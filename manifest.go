package vaultzero

import "time"

type Manifest struct {
	files       []File `json:"files"`
	fileNameMap map[string]*File
	hashMap     map[string][]*File
}

type File struct {
	Path         string    `json:"path"`
	ModifiedTime time.Time `json:"modified_time"`
	Size         int64     `json:"size"`
	Hash         string    `json:"hash"`
	Found        bool
}
