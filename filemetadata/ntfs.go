package filemetadata

import (
	"fmt"
	"os"
)

type NTFSFileMetadataProvider struct{}

func (p *NTFSFileMetadataProvider) GetFileMetadata(path string) (*FileMetadata, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("ntfsmetadataprovider: failed to get file metadata: %w", err)
	}

	var vaultzeroID *string
	vaultzeroIDRaw, err := os.ReadFile(path + ":vaultzero")
	if err == nil {
		vaultzeroIDStr := string(vaultzeroIDRaw)
		vaultzeroID = &vaultzeroIDStr
	} else {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("ntfsmetadataprovider: failed to read vaultzero id: %w", err)
		} 
	}

	return &FileMetadata{
		Size:         info.Size(),
		LastModified: info.ModTime(),
		VaultZeroID:  vaultzeroID,
	}, nil
}

func (p *NTFSFileMetadataProvider) SetFileMetadata(path string, metadata FileMetadata) error {
	return os.WriteFile(path+":vaultzero", []byte(*metadata.VaultZeroID), 0644)
}
