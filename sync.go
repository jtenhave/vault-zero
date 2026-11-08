package vaultzero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	vzaws "github.com/jtenhave/vaultzero/aws"
	"github.com/jtenhave/vaultzero/filemetadata"
)

type uploadJob struct {
	filePath     string
	uploadFile   bool
	key          *string
	size         int64
	lastModified time.Time
}

type syncState struct {
	archiveID            string
	s3Client             *s3.Client
	bucketName           string
	manifest             *Manifest
	manifestLock         sync.Mutex
	fileMetadataProvider filemetadata.FileMetadataProvider
	uploadChannel        chan *uploadJob
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

	syncState := &syncState{
		archiveID:            archiveFileData.ID,
		s3Client:             s3Client,
		bucketName:           settingsFileData.BucketName,
		fileMetadataProvider: fileMetadataProvider,
		uploadChannel:        make(chan *uploadJob, 100),
	}

	err = syncState.loadManifest(ctx)
	if err != nil {
		return fmt.Errorf("sync: failed to download manifest: %w", err)
	}

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
		filePath := filepath.Join(folder, localFile.Name())
		if localFile.IsDir() {
			folders = append(folders, filePath)
			continue
		}

		localFileInfo, err := localFile.Info()
		if err != nil {
			return fmt.Errorf("failed to get file info: %s error: %w", filePath, err)
		}

		encodedFilePath := url.QueryEscape(filePath)

		// Check if file is tracked in the manifest
		trackedFile, ok := syncState.manifest.fileNameMap[encodedFilePath]
		uploadFile := false
		if ok {
			if trackedFile.Size == localFileInfo.Size() || trackedFile.ModifiedTime == localFileInfo.ModTime() {
				continue
			} else {
				uploadFile = true
			}
		} else {
			fileHash, err := syncState.hashFile(encodedFilePath)
			if err != nil {
				log.Printf("failed to hash file: %v", filePath, err)
				continue
			}

			trackedFile, err = syncState.findMovedFile(fileHash)
			if err != nil {
				log.Printf("failed to find moved file: %v", filePath, err)
				continue
			}

			if trackedFile == nil {
				uploadFile = true
			}
		}

		syncState.uploadChannel <- &uploadJob{
			manifestFile: trackedFile,
			filePath:     encodedFilePath,
			uploadFile:   uploadFile,
		}
	}

	for _, folder := range folders {
		err = syncState.syncFolder(ctx, folder)
		if err != nil {
			return fmt.Errorf("failed to sync folder: %s error: %w", folder, err)
		}
	}

	return nil
}

func (syncState *syncState) startUploadFileWorker(ctx context.Context, uploadChannel chan *uploadJob) (err error) {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case writeObject := <-uploadChannel:
			err = syncState.uploadFile(ctx, writeObject)
			if err != nil {
				log.Printf("sync.uploadFile: failed to upload file: %s error: %v", writeObject.filePath, err)
			}
		}
	}
}

func (syncState *syncState) uploadFile(ctx context.Context, uploadJob *uploadJob) (err error) {
	fileMetadata, err := syncState.fileMetadataProvider.GetFileMetadata(uploadJob.filePath)
	if err != nil {
		return fmt.Errorf("sync.uploadFile: failed to get file metadata: %s error: %w", uploadJob.filePath, err)
	}

	if fileMetadata == nil && uploadJob.key != nil {
		log.Printf("sync.uploadFile: file metadata does not exist for file but key exists: %s", uploadJob.filePath)
	}

	if fileMetadata != nil && uploadJob.key == nil {
		log.Printf("sync.uploadFile: file metadata exists for file but key does not exist: %s", uploadJob.filePath)
	}

	if fileMetadata != nil && uploadJob.key != nil {
		if fileMetadata.VaultZeroID != *uploadJob.key {
			return fmt.Errorf("sync.uploadFile: file metadata exists for file but key does not match: %s", uploadJob.filePath)
		}
	}

	var key string
	if uploadJob.key != nil {
		key = *uploadJob.key
	} else {
		key = uuid.New().String()
	}

	if uploadJob.uploadFile {
		file, err := os.Open(uploadJob.filePath)
		if err != nil {
			return fmt.Errorf("failed to open file: %s error: %w", uploadJob.filePath, err)
		}
		defer file.Close()

		_, err = syncState.s3Client.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(syncState.bucketName),
			Key:    aws.String(key),
			Body:   file,
		})
		if err != nil {
			return fmt.Errorf("sync.uploadFile: failed to put object: %s error: %w", uploadJob.filePath, err)
		}
	}

	err = syncState.fileMetadataProvider.SetFileMetadata(uploadJob.filePath, filemetadata.FileMetadata{
		Size:         uploadJob.size,
		LastModified: uploadJob.lastModified,
		VaultZeroID:  key,
	})

	if err != nil {
		return fmt.Errorf("sync.uploadFile: failed to set file metadata: %s error: %w", uploadJob.filePath, err)
	}

	storedPath := toStoredPath(uploadJob.filePath)

	writeManifest := false
	if currentStoredPath, ok := syncState.manifest.Files[key]; ok {
		if currentStoredPath != storedPath {
			writeManifest = true
		}
	} else {
		writeManifest = true
	}

	if writeManifest {
		syncState.manifestLock.Lock()
		defer syncState.manifestLock.Unlock()

		syncState.manifest.Files[key] = storedPath
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

func toStoredPath(filePath string) string {

	return filePath
}

func fromStoredPath(rootpath, storedPath string) string {
	return storedPath
}
