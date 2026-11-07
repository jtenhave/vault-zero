package filemetadata

import "time"

type FileMetadata struct {
	Size         int64
	LastModified time.Time
	VaultZeroID  *string
}

type FileMetadataProvider interface {
	GetFileMetadata(path string) (FileMetadata, error)
	SetFileMetadata(path string, metadata FileMetadata) error
}