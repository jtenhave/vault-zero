package vaultzero

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	vzaws "github.com/jtenhave/vaultzero/aws"
)

type syncState struct {
	archiveID  string
	s3Client   *s3.Client
	manifest   *Manifest
	bucketName string
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

	for _, localFile := range localFiles {
		filePath := filepath.Join(folder, localFile.Name())
		base64FilePath := base64.StdEncoding.EncodeToString([]byte(filePath))

		// STEP 1: Check if file is a directory
		if localFile.IsDir() {
			return syncState.syncFolder(ctx, filePath)
		}

		// STEP 2: Check if file exists in the manifest
		trackedFile, ok := syncState.manifest.fileNameMap[base64FilePath]
		if ok {
			// STEP 3: Check if file is up to date
			fileInfo, err := localFile.Info()
			if err != nil {
				return fmt.Errorf("failed to get file info: %s error: %w", localFile.Name(), err)
			}

			if trackedFile.ModifiedTime.After(fileInfo.ModTime()) {
				// STEP 4: Upload file
				// UPLOAD FILE
			}
		} else {
			localFileHash, err := hashFile(filePath)
			if err != nil {
				return fmt.Errorf("failed to hash file: %s error: %w", localFile.Name(), err)
			}

			if localFileHash != trackedFile.Hash {
				// STEP 4: Upload file
				// UPLOAD FILE
			}
		}
	}

	return nil
}

func hashFile(filePath string) (string, error) {
	localFile, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %s error: %w", filePath, err)
	}
	defer localFile.Close()

	hash := sha256.New()
	_, err = io.Copy(hash, localFile)
	if err != nil {
		return "", fmt.Errorf("failed to hash file: %s error: %w", filePath, err)
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
