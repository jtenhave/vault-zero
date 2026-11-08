package filemetadata

import (
	"encoding/json"
	"fmt"
	"os"
)

type ntfsFileMetadataProvider struct{}

func (p *ntfsFileMetadataProvider) GetFileMetadata(path string) (*FileMetadata, error) {
	fileMetadataRaw, err := os.ReadFile(path + ":vaultzero")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("ntfsmetadataprovider.GetFileMetadata: failed to read vaultzero id: %w", err)
	}

	var fileMetadata FileMetadata
	err = json.Unmarshal(fileMetadataRaw, &fileMetadata)
	if err != nil {
		return nil, fmt.Errorf("ntfsmetadataprovider.GetFileMetadata: failed to unmarshal file metadata: %w", err)
	}

	return &fileMetadata, nil
}

func (p *ntfsFileMetadataProvider) SetFileMetadata(path string, metadata FileMetadata) error {
	fileMetadataRaw, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("ntfsmetadataprovider.SetFileMetadata: failed to marshal file metadata: %w", err)
	}

	return os.WriteFile(path+":vaultzero", fileMetadataRaw, 0644)
}
