package vaultzero

import (
	"errors"
	"fmt"
	"os"
)

const archiveFileName = ".vaultzero"
const manifestFileName = "manifest.json"

func getManifestFileKey(archiveID string) string {
	return fmt.Sprintf("%s/%s", archiveID, manifestFileName)
}

func writeNewLocalFile(filepath string, data []byte) error {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("file already exists")
		} else {
			return fmt.Errorf("failed to create file: %w", err)
		}
	}

	defer file.Close()

	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}
