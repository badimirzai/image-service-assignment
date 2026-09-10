package store

import (
	"errors"
	"sync"
)

var ErrNotFound = errors.New("resource not found")

// Metadata is the shared image metadata representation (API + persistence fields).
type Metadata struct {
	ID         int64  `json:"id"`
	Filesize   int64  `json:"filesize"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	ImageType  string `json:"image_type"`
	UploadDate string `json:"upload_date"`
	Filename   string `json:"filename,omitempty"`
}

// ImageRecord is the persistence model: metadata plus opaque image bytes.
type ImageRecord struct {
	Metadata Metadata
	Bytes    []byte
}

// Store is the persistence boundary. Concrete engines (memory, later SQLite)
// implement this interface; the service depends only on Store.
type Store interface {
	// Methods will be added as API functionality is implemented.
}

// MemoryStore is a concurrency-safe in-memory Store.
type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int64
	records map[int64]ImageRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID:  1,
		records: make(map[int64]ImageRecord),
	}
}
