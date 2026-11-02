package vaultzero

import "time"

type Manifest struct {
	files       []*File `json:"files"`
	fileNameMap map[string]*File
	hashMap     map[string][]*File
}

type File struct {
	Path         string    `json:"path"`
	ModifiedTime time.Time `json:"modified_time"`
	Hash         string    `json:"hash"`
}
