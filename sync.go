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

type syncState struct {
	archiveID        string
	s3Client         *s3.Client
	manifest         *Manifest
	bucketName       string
	localFileHashMap map[string]string
}

func Sync(ctx context.Context, rootFolder string) (err error) {
	archiveFilePath := filepath.Join(rootFolder, ".vaultzero")
	archiveFileRaw, err := os.ReadFile(archiveFilePath)
	if err != nil {
		return fmt.Errorf("failed to read archive file: %w", err)
	}

	var archiveFileData ArchiveFileData
	err = json.Unmarshal(archiveFileRaw, &archiveFileData)
	if err != nil {
		return fmt.Errorf("failed to unmarshal archive file data: %w", err)
	}

	archiveID := archiveFileData.ID
	if archiveID == "" {
		return fmt.Errorf("archive ID is empty")
	}

	dataDirFilePath, err := getDataDirFilePath()
	if err != nil {
		return fmt.Errorf("failed to get data dir file path: %w", err)
	}

	dataDirFileRaw, err := os.ReadFile(dataDirFilePath)
	if err != nil {
		return fmt.Errorf("failed to read data dir file: %w", err)
	}

	var dataDirFileData DataDirFileData
	err = json.Unmarshal(dataDirFileRaw, &dataDirFileData)
	if err != nil {
		return fmt.Errorf("failed to unmarshal data dir file data: %w", err)
	}

	if dataDirFileData.AccessKey == "" || dataDirFileData.SecretAccessKey == "" || dataDirFileData.BucketName == "" {
		return fmt.Errorf("data dir file data is missing required fields")
	}

	s3Client, err := vzaws.NewS3Client(ctx, dataDirFileData.AccessKey, dataDirFileData.SecretAccessKey)
	if err != nil {
		return fmt.Errorf("failed to create S3 client: %w", err)
	}

	syncState := &syncState{
		archiveID:  archiveID,
		s3Client:   s3Client,
		bucketName: dataDirFileData.BucketName,
	}

	err = syncState.loadManifest(ctx)
	if err != nil {
		return fmt.Errorf("failed to download manifest: %w", err)
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

	for _, file := range manifest.files {
		syncState.manifest.fileNameMap[file.Path] = file
		syncState.manifest.hashMap[file.Hash] = append(syncState.manifest.hashMap[file.Hash], file)
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
		if ok {
			if trackedFile.Size == localFileInfo.Size() && trackedFile.ModifiedTime == localFileInfo.ModTime() {
				trackedFile.Found = true
				continue
			}
		}

		fileHash, err := syncState.hashFile(encodedFilePath)
		if err != nil {
			log.Printf("failed to hash file: %v", filePath, err)
			continue
		}

		equivalentTrackedFile, err := syncState.findMovedFile(fileHash)
		if err != nil {
			return fmt.Errorf("failed to find moved file: %s error: %w", filePath, err)
		}

		if equivalentTrackedFile == nil {
			// NEW
			// UPLOAD FILE
			// UPDATE MANIFEST
			continue
		}


		// MOVED
		// UPDATE MANIFEST
		equivalentTrackedFile.Found = true
	}

	// Do folders last.
	for _, folder := range folders {
		err = syncState.syncFolder(ctx, folder)
		if err != nil {
			return fmt.Errorf("failed to sync folder: %s error: %w", folder, err)
		}
	}

	return nil
}

func (syncState *syncState) findMovedFile(fileHash string) (*File, error) {
	equivalentTrackedFiles, ok := syncState.manifest.hashMap[fileHash]
	if ok {
		for _, equivalentTrackedFile := range equivalentTrackedFiles {
			localEquivalentFilePath, err := url.QueryUnescape(equivalentTrackedFile.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to decode equivalent file path: %s error: %w", equivalentTrackedFile.Path, err)
			}

			_, err = os.Stat(localEquivalentFilePath)
			if err != nil {
				if os.IsNotExist(err) {
					return equivalentTrackedFile, nil
				}

				return nil, fmt.Errorf("failed to stat file: %s error: %w", localEquivalentFilePath, err)
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
