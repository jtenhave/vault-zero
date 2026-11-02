package vaultzero

import (
	"fmt"
	"os"
)

const archiveFileName = ".vaultzero"
const manifestFileName = "manifest.json"
const dataDir = "VaultZero"

type ArchiveFileData struct {
	ID string `json:"id"`
}

type DataDirFileData struct {
	ID              string `json:"id"`
	BucketName      string `json:"bucket_name"`
	AccessKey       string `json:"access_key"`
	SecretAccessKey string `json:"secret_access_key"`
}

func getDataDirFilePath() (string, error) {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user config dir: %s", err)
	}

	return fmt.Sprintf("%s\\%s", userConfigDir, dataDir), nil
}

func getManifestFileKey(archiveID string) string {
	return fmt.Sprintf("%s/%s", archiveID, manifestFileName)
}
