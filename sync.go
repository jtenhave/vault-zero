package vaultzero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	vzaws "github.com/jtenhave/vaultzero/aws"
	"github.com/jtenhave/vaultzero/filemetadata"
	"github.com/jtenhave/vaultzero/filewritelock"
)

type syncState struct {
	archiveID            string
	s3Client             *s3.Client
	bucketName           string
	rootPath             string
	manifest             *Manifest
	manifestLock         sync.RWMutex
	foundKeys            map[string]bool
	foundKeysLock        sync.RWMutex
	fileMetadataProvider filemetadata.FileMetadataProvider
	processChannel       chan string
}

func Sync(ctx context.Context, rawRootFolder string) (err error) {
	rootFolder, err := filepath.Abs(rawRootFolder)
	if err != nil {
		return fmt.Errorf("sync: failed to parse root folder: %w", err)
	}

	archiveFileData, err := getArchiveFileData(rootFolder)
	if err != nil {
		return fmt.Errorf("sync: failed to get archive file data: %w", err)
	}

	settingsFileData, err := getSettingsFileData(archiveFileData.ID)
	if err != nil {
		return fmt.Errorf("sync: failed to get settings file data: %w", err)
	}

	s3Client, err := vzaws.NewS3Client(ctx, settingsFileData.AccessKey, settingsFileData.SecretAccessKey)
	if err != nil {
		return fmt.Errorf("sync: failed to create S3 client: %w", err)
	}

	fileMetadataProvider, err := filemetadata.GetFileMetaDataProvider(rootFolder)
	if err != nil {
		return fmt.Errorf("sync: failed to get file metadata provider: %w", err)
	}

	uploadChannel := make(chan string, 100)

	syncState := &syncState{
		archiveID:            archiveFileData.ID,
		s3Client:             s3Client,
		bucketName:           settingsFileData.BucketName,
		rootPath:             rootFolder,
		foundKeys:            make(map[string]bool),
		fileMetadataProvider: fileMetadataProvider,
		processChannel:       uploadChannel,
	}

	err = syncState.loadManifest(ctx)
	if err != nil {
		return fmt.Errorf("sync: failed to download manifest: %w", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			syncState.startUploadFileWorker(ctx, uploadChannel, i)
		}()
	}

	err = syncState.syncFolder(ctx, rootFolder)

	close(uploadChannel)
	wg.Wait()

	if err == nil {
		syncState.manifestLock.Lock()

		for key := range syncState.manifest.Files {
			if !syncState.foundKeys[key] {
				delete(syncState.manifest.Files, key)
			}
		}

		err = syncState.uploadManifest(ctx)
		if err != nil {
			return fmt.Errorf("sync: failed to upload manifest: %w", err)
		}
	}
	return err
}

func (syncState *syncState) loadManifest(ctx context.Context) (err error) {
	object, err := syncState.s3Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(syncState.bucketName),
		Key:    aws.String(getManifestFileKey(syncState.archiveID)),
	})
	if err != nil {
		return fmt.Errorf("failed to get manifest: %w", err)
	}

	manifestBytes, err := io.ReadAll(object.Body)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest Manifest
	err = json.Unmarshal(manifestBytes, &manifest)
	if err != nil {
		return fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	if manifest.Files == nil {
		manifest.Files = make(map[string]string)
	}

	syncState.manifest = &manifest

	return nil
}

func (syncState *syncState) syncFolder(ctx context.Context, folder string) (err error) {
	info, err := os.Stat(folder)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("folder does not exist: %s", folder)
		}

		return fmt.Errorf("failed to stat folder: %s error: %w", folder, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("folder is not a directory: %s", folder)
	}

	localFiles, err := os.ReadDir(folder)
	if err != nil {
		return fmt.Errorf("failed to read folder: %s error: %w", folder, err)
	}

	folders := make([]string, 0)
	for _, localFile := range localFiles {

		fileName := localFile.Name()
		if fileName == archiveFileName {
			continue
		}

		filePath := filepath.Join(folder, fileName)
		if localFile.IsDir() {
			folders = append(folders, filePath)
			continue
		}

		syncState.processChannel <- filePath
	}

	var folderError error
	for _, folder := range folders {
		err = syncState.syncFolder(ctx, folder)
		if err != nil {
			folderError = err
			log.Printf("failed to sync folder: %s error: %v", folder, err)
		}
	}

	return folderError
}

func (syncState *syncState) startUploadFileWorker(ctx context.Context, processChannel chan string, workerID int) (err error) {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case filePath, ok := <-processChannel:
			if !ok {
				return nil
			}

			err = syncState.processFile(ctx, filePath, workerID)
			if err != nil {
				log.Printf("sync.uploadFile: failed to upload file: %s error: %v", filePath, err)
			}
		}
	}
}

func (syncState *syncState) processFile(ctx context.Context, filePath string, workerID int) (err error) {
	writeLock, err := filewritelock.GetFileWriteLock(filePath)
	if err != nil {
		return fmt.Errorf("sync.processFile: failed to get write lock: %s error: %w", filePath, err)
	}
	defer filewritelock.ReleaseFileWriteLock(writeLock)

	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("sync.processFile: failed to stat file: %s error: %w", filePath, err)
	}

	currentSize := fileInfo.Size()
	currentLastModified := fileInfo.ModTime()

	fileMetadata, err := syncState.fileMetadataProvider.GetFileMetadata(filePath)
	if err != nil {
		return fmt.Errorf("sync.processFile: failed to get file metadata: %s error: %w", filePath, err)
	}

	uploadFile := false
	uploadManifest := false
	vaultPath := syncState.toVaultPath(filePath)
	vaultZeroID := ""

	if fileMetadata == nil {
		uploadFile = true
		uploadManifest = true
		vaultZeroID = uuid.New().String()
		log.Printf("sync.processFile: new file: %s", vaultPath)
	} else {
		vaultZeroID = fileMetadata.VaultZeroID
		if fileMetadata.Size != currentSize || fileMetadata.LastModified != currentLastModified {
			uploadFile = true
		}

		if fileMetadata.VaultPath != vaultPath {
			uploadManifest = true
		}

		syncState.manifestLock.RLock()
		if _, ok := syncState.manifest.Files[vaultZeroID]; !ok {
			uploadManifest = true
		}
		syncState.manifestLock.RUnlock()

		if uploadFile && uploadManifest {
			log.Printf("sync.processFile: existing file moved and changed: %s", vaultPath)
		} else if uploadFile {
			log.Printf("sync.processFile: existing file changed: %s", vaultPath)
		} else if uploadManifest {
			log.Printf("sync.processFile: existing file moved: %s", vaultPath)
		} else {
			log.Printf("sync.processFile: existing file no changes: %s", vaultPath)
		}
	}

	syncState.foundKeysLock.Lock()
	syncState.foundKeys[vaultZeroID] = true
	syncState.foundKeysLock.Unlock()

	if uploadFile {
		log.Printf("sync.processFile: starting upload of file: %s on worker ID: %d", filePath, workerID)

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %s error: %w", filePath, err)
		}
		defer file.Close()

		_, err = syncState.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:       aws.String(syncState.bucketName),
			Key:          aws.String(getArchiveFileKey(syncState.archiveID, vaultZeroID)),
			Body:         file,
			StorageClass: types.StorageClassDeepArchive,
		})
		if err != nil {
			return fmt.Errorf("sync.processFile: failed to put object: %s error: %w", filePath, err)
		}

		log.Printf("sync.processFile: finished upload of file: %s on worker ID: %d", filePath, workerID)
	}

	// Only set this after upload is successful. That way an upload failure doesn't leave the file metadata in an inconsistent state.
	err = syncState.fileMetadataProvider.SetFileMetadata(filePath, filemetadata.FileMetadata{
		VaultPath:    vaultPath,
		Size:         currentSize,
		LastModified: currentLastModified,
		VaultZeroID:  vaultZeroID,
	})

	if err != nil {
		return fmt.Errorf("sync.processFile: failed to set file metadata: %s error: %w", filePath, err)
	}

	err = os.Chtimes(filePath, currentLastModified, currentLastModified)
	if err != nil {
		return fmt.Errorf("sync.processFile: failed to change file times: %s error: %w", filePath, err)
	}

	if uploadManifest {
		syncState.manifestLock.Lock()
		syncState.manifest.Files[vaultZeroID] = vaultPath
		err = syncState.uploadManifest(ctx)
		if err != nil {
			return fmt.Errorf("sync.processFile: failed to upload manifest: %w", err)
		}
	}

	return nil
}

func (syncState *syncState) toVaultPath(filePath string) string {
	rawPath := strings.TrimPrefix(filePath, syncState.rootPath)
	return strings.ReplaceAll(rawPath, "\\", "/")
}

func (syncState *syncState) uploadManifest(ctx context.Context) (err error) {
	manifestBytes, err := json.Marshal(syncState.manifest)
	syncState.manifestLock.Unlock()
	if err != nil {
		return fmt.Errorf("sync: failed to marshal manifest: %w", err)
	}

	_, err = syncState.s3Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(syncState.bucketName),
		Key:    aws.String(getManifestFileKey(syncState.archiveID)),
		Body:   bytes.NewReader(manifestBytes),
	})

	if err != nil {
		return fmt.Errorf("sync: failed to put manifest: %w", err)
	}
	return nil
}
