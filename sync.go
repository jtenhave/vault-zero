package vaultzero

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	vzaws "github.com/jtenhave/vaultzero/aws"
)

type writeObject struct {
	manifestFile    *File
	encodedFilePath string
	uploadFile      bool
}

type syncState struct {
	archiveID        string
	s3Client         *s3.Client
	manifest         *Manifest
	bucketName       string
	localFileHashMap map[string]string
	uploadChannel    chan *writeObject
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
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	syncState := &syncState{
		archiveID:  archiveFileData.ID,
		s3Client:   s3Client,
		bucketName: settingsFileData.BucketName,
	}

	err = syncState.loadManifest(ctx)
	if err != nil {
		return fmt.Errorf("failed to download manifest: %w", err)
	}

	syncState.uploadChannel = make(chan *writeObject, 100)

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

	for _, file := range manifest.files {
		syncState.manifest.fileNameMap[file.Path] = &file
		syncState.manifest.hashMap[file.Hash] = append(syncState.manifest.hashMap[file.Hash], &file)
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

		syncState.uploadChannel <- &writeObject{
			manifestFile:    trackedFile,
			encodedFilePath: encodedFilePath,
			uploadFile:      uploadFile,
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

func (syncState *syncState) startUploadFileWorker(ctx context.Context, uploadChannel chan *writeObject) (err error) {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case writeObject := <-uploadChannel:

		}
	}

	return nil
}

func (syncState *syncState) findMovedFile(fileHash string) (*File, error) {
	trackedFiles, ok := syncState.manifest.hashMap[fileHash]
	if ok {
		for _, trackedFile := range trackedFiles {
			localFilePath, err := url.QueryUnescape(trackedFile.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to decode tracked file path: %s error: %w", trackedFile.Path, err)
			}

			_, err = os.Stat(localFilePath)
			if err != nil {
				if os.IsNotExist(err) {
					return trackedFile, nil
				}

				return nil, fmt.Errorf("failed to stat file: %s error: %w", localFilePath, err)
			}
		}
	}

	return nil, nil
}

func (syncState *syncState) hashFile(encodedFilePath string) (string, error) {
	hash, ok := syncState.localFileHashMap[encodedFilePath]
	if ok {
		return hash, nil
	}

	filePath, err := url.QueryUnescape(encodedFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to decode file path: %s error: %w", encodedFilePath, err)
	}

	localFile, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %s error: %w", filePath, err)
	}
	defer localFile.Close()

	newHash := sha256.New()
	_, err = io.Copy(newHash, localFile)
	if err != nil {
		return "", fmt.Errorf("failed to hash file: %s error: %w", filePath, err)
	}

	hash = hex.EncodeToString(newHash.Sum(nil))
	syncState.localFileHashMap[encodedFilePath] = hash
	return hash, nil
}
