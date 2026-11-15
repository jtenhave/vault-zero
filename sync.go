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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	vzaws "github.com/jtenhave/vaultzero/aws"
	"github.com/jtenhave/vaultzero/filemetadata"
)

type uploadJob struct {
	filePath       string
	vaultPath      string
	key            string
	size           int64
	lastModified   time.Time
	uploadManifest bool
	uploadFile     bool
}

type syncState struct {
	archiveID            string
	s3Client             *s3.Client
	bucketName           string
	rootPath             string
	manifest             *Manifest
	manifestLock         sync.Mutex
	fileMetadataProvider filemetadata.FileMetadataProvider
	uploadChannel        chan uploadJob
}

func Sync(ctx context.Context, rootFolder string) (err error) {
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

	uploadChannel := make(chan uploadJob, 100)

	syncState := &syncState{
		archiveID:            archiveFileData.ID,
		s3Client:             s3Client,
		bucketName:           settingsFileData.BucketName,
		rootPath:             rootFolder,
		fileMetadataProvider: fileMetadataProvider,
		uploadChannel:        uploadChannel,
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
			syncState.startUploadFileWorker(ctx, uploadChannel)
		}()
	}

	syncState.syncFolder(ctx, rootFolder)
	if err != nil {
		return fmt.Errorf("sync: failed to sync folder: %w", err)
	}

	close(uploadChannel)

	wg.Wait()

	return nil
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

		localFileInfo, err := localFile.Info()
		if err != nil {
			log.Printf("failed to get file info: %s error: %v", filePath, err)
			continue
		}

		fileMetadata, err := syncState.fileMetadataProvider.GetFileMetadata(filePath)
		if err != nil {
			log.Printf("failed to get file metadata: %s error: %v", filePath, err)
			continue
		}

		currentSize := localFileInfo.Size()
		currentLastModified := localFileInfo.ModTime()

		uploadJob := uploadJob{
			filePath:       filePath,
			vaultPath:      syncState.toVaultPath(filePath),
			size:           currentSize,
			lastModified:   currentLastModified,
			uploadManifest: false,
			uploadFile:     false,
		}

		if fileMetadata == nil {
			uploadJob.uploadFile = true
			uploadJob.uploadManifest = true
			uploadJob.key = uuid.New().String()
			log.Printf("sync.syncFolder: new file: %s", uploadJob.vaultPath)
		} else {
			uploadJob.key = fileMetadata.VaultZeroID
			if fileMetadata.Size != currentSize || fileMetadata.LastModified != currentLastModified {
				uploadJob.uploadFile = true
			}

			if fileMetadata.VaultPath != uploadJob.vaultPath {
				uploadJob.uploadManifest = true
			}

			if uploadJob.uploadFile && uploadJob.uploadManifest {
				log.Printf("sync.syncFolder: existing file moved and changed: %s", uploadJob.vaultPath)
			} else if uploadJob.uploadFile {
				log.Printf("sync.syncFolder: existing file changed: %s", uploadJob.vaultPath)
			} else if uploadJob.uploadManifest {
				log.Printf("sync.syncFolder: existing file moved: %s", uploadJob.vaultPath)
			}
		}

		if uploadJob.uploadFile || uploadJob.uploadManifest {
			syncState.uploadChannel <- uploadJob
		}
	}

	for _, folder := range folders {
		err = syncState.syncFolder(ctx, folder)
		if err != nil {
			log.Printf("failed to sync folder: %s error: %v", folder, err)
		}
	}

	return nil
}

func (syncState *syncState) startUploadFileWorker(ctx context.Context, uploadChannel chan uploadJob) (err error) {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case uploadJob, ok := <-uploadChannel:
			if !ok {
				return nil
			}

			err = syncState.uploadFile(ctx, uploadJob)
			if err != nil {
				log.Printf("sync.uploadFile: failed to upload file: %s error: %v", uploadJob.filePath, err)
			}
		}
	}
}

func (syncState *syncState) uploadFile(ctx context.Context, uploadJob uploadJob) (err error) {
	if uploadJob.uploadFile {
		file, err := os.Open(uploadJob.filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %s error: %w", uploadJob.filePath, err)
		}
		defer file.Close()

		_, err = syncState.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket:       aws.String(syncState.bucketName),
			Key:          aws.String(getArchiveFileKey(syncState.archiveID, uploadJob.key)),
			Body:         file,
			StorageClass: types.StorageClassDeepArchive,
		})
		if err != nil {
			return fmt.Errorf("sync.uploadFile: failed to put object: %s error: %w", uploadJob.filePath, err)
		}
	}

	// Only set this after upload is successful. That way an upload failure doesn't leave the file metadata in an inconsistent state.
	err = syncState.fileMetadataProvider.SetFileMetadata(uploadJob.filePath, filemetadata.FileMetadata{
		VaultPath:    uploadJob.vaultPath,
		Size:         uploadJob.size,
		LastModified: uploadJob.lastModified,
		VaultZeroID:  uploadJob.key,
	})

	if err != nil {
		return fmt.Errorf("sync.uploadFile: failed to set file metadata: %s error: %w", uploadJob.filePath, err)
	}

	if uploadJob.uploadManifest {
		syncState.manifestLock.Lock()
		defer syncState.manifestLock.Unlock()

		syncState.manifest.Files[uploadJob.key] = uploadJob.vaultPath
		manifestBytes, err := json.Marshal(syncState.manifest)
		if err != nil {
			return fmt.Errorf("sync.uploadFile: failed to marshal manifest: %w", err)
		}

		_, err = syncState.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(syncState.bucketName),
			Key:    aws.String(getManifestFileKey(syncState.archiveID)),
			Body:   bytes.NewReader(manifestBytes),
		})

		if err != nil {
			return fmt.Errorf("sync.uploadFile: failed to put manifest: %w", err)
		}
	}

	return nil
}

func (syncState *syncState) toVaultPath(filePath string) string {
	return strings.TrimPrefix(filePath, syncState.rootPath)
}
