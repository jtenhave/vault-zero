package filemetadata

import (
	"fmt"
	"time"
)

type FileMetadata struct {
	ArchiveID    string    `json:"archiveId"`
	VaultPath    string    `json:"path"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
	VaultZeroID  string    `json:"vaultzeroId"`
}

type FileMetadataProvider interface {
	GetFileMetadata(path string) (*FileMetadata, error)
	SetFileMetadata(path string, metadata FileMetadata) error
}

func GetFileMetaDataProvider(path string) (FileMetadataProvider, error) {
	filesystemType, err := getFilesystemType(path)
	if err != nil {
		return nil, fmt.Errorf("filemetadata.GetFileMetaDataProvider: failed to get filesystem type: %w", err)
	}

	switch filesystemType {
	case "NTFS":
		return &ntfsFileMetadataProvider{}, nil
	}

	return nil, fmt.Errorf("filemetadata.GetFileMetaDataProvider: unsupported filesystem type: %s", filesystemType)
}
