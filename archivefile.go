package vaultzero

import (
	"encoding/json"
	"fmt"
	"os"
)

type archiveFileData struct {
	ID string `json:"id"`
}

func getArchiveFilePath(rootFolder string) string {
	return fmt.Sprintf("%s\\%s", rootFolder, archiveFileName)
}

func getArchiveFileData(rootFolder string) (*archiveFileData, error) {
	archiveFilePath := getArchiveFilePath(rootFolder)

	_, err := os.Stat(archiveFilePath)
	if err != nil {

		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("archivefile.getArchiveFileData: failed to stat archive file: %w", err)
	}

	archiveFileRaw, err := os.ReadFile(archiveFilePath)
	if err != nil {
		return nil, fmt.Errorf("archivefile.getArchiveFileData: failed to read archive file: %w", err)
	}

	var archiveFileData archiveFileData
	err = json.Unmarshal(archiveFileRaw, &archiveFileData)
	if err != nil {
		return nil, fmt.Errorf("archivefile.getArchiveFileData: failed to unmarshal archive file data: %w", err)
	}

	archiveID := archiveFileData.ID
	if archiveID == "" {
		return nil, fmt.Errorf("archivefile.getArchiveFileData: archive ID is empty")
	}

	return &archiveFileData, nil
}

func setArchiveFileData(rootFolder string, archiveID string) (func() error, error) {
	archiveFilePath := getArchiveFilePath(rootFolder)

	archiveFileData := archiveFileData{
		ID: archiveID,
	}

	archiveFileDataBytes, err := json.Marshal(archiveFileData)
	if err != nil {
		return nil, fmt.Errorf("archivefile.setArchiveFileData: error marshaling metadata file to JSON: %w", err)
	}

	err = writeNewLocalFile(archiveFilePath, archiveFileDataBytes)
	if err != nil {
		return nil, fmt.Errorf("archivefile.setArchiveFileData: failed to write archive file: %w", err)
	}

	return func() error {
		err := os.Remove(archiveFilePath)
		if err != nil {
			return fmt.Errorf("archivefile.setArchiveFileData: failed to delete archive file: %w", err)
		}

		return nil
	}, nil
}
