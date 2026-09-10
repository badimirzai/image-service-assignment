// Package store defines the persistence boundary for image records.
// The service depends on Store; handlers never talk to storage directly.
package store

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ErrNotFound is returned when an image ID does not exist in the store.
var ErrNotFound = errors.New("resource not found")

// Metadata is the shared image metadata representation used both in API
// responses and inside persisted records. Filename is optional (e.g. absent
// for raw-body POST uploads).
type Metadata struct {
	ID         int64  `json:"id"`
	Filesize   int64  `json:"filesize"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	ImageType  string `json:"image_type"`
	UploadDate string `json:"upload_date"`
	Filename   string `json:"filename,omitempty"`
}

// ImageRecord is one stored image: derived metadata plus the original bytes.
// Bytes are opaque to the store - it does not decode or validate images.
type ImageRecord struct {
	Metadata Metadata
	Bytes    []byte // original upload payload; never mutated in place by callers
}

// Store is the persistence boundary. MemoryStore is the first implementation;
// a SQLite-backed Store can be added later without changing the service API.
type Store interface {
	// Create assigns a server-side integer ID, persists data unchanged, and
	// returns the stored metadata (including the new ID).
	Create(ctx context.Context, meta Metadata, data []byte) (Metadata, error)
	// Get returns one image record (metadata + original bytes) by ID.
	Get(ctx context.Context, id int64) (ImageRecord, error)
	// List returns metadata for all images (no bytes). Empty store → empty slice.
	List(ctx context.Context) ([]Metadata, error)
}

// MemoryStore is a concurrency-safe in-memory Store.
// Suitable for local demo/eval use; data is lost when the process exits.
type MemoryStore struct {
	mu      sync.RWMutex
	nextID  int64                 // monotonically increasing; starts at 1
	records map[int64]ImageRecord // keyed by assigned ID
}

// NewMemoryStore returns an empty in-memory store ready for concurrent use.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID:  1,
		records: make(map[int64]ImageRecord),
	}
}

// Compile-time check that MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)

// Create assigns the next integer ID and stores a defensive copy of data so
// later mutations by the caller cannot affect what is persisted.
func (s *MemoryStore) Create(ctx context.Context, meta Metadata, data []byte) (Metadata, error) {
	if err := ctx.Err(); err != nil {
		return Metadata{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	meta.ID = s.nextID
	s.nextID++

	// Copy bytes so the store owns its own slice.
	cp := make([]byte, len(data))
	copy(cp, data)

	s.records[meta.ID] = ImageRecord{
		Metadata: meta,
		Bytes:    cp,
	}
	return meta, nil
}

// Get returns the stored record for id, or ErrNotFound.
func (s *MemoryStore) Get(ctx context.Context, id int64) (ImageRecord, error) {
	if err := ctx.Err(); err != nil {
		return ImageRecord{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.records[id]
	if !ok {
		return ImageRecord{}, ErrNotFound
	}
	// Return a copy of bytes so callers cannot mutate the store's slice.
	cp := make([]byte, len(rec.Bytes))
	copy(cp, rec.Bytes)
	out := rec
	out.Bytes = cp
	return out, nil
}

// List returns metadata for all images, newest (highest ID) first.
// Image bytes are not included - callers that need data will use a Get path later.
func (s *MemoryStore) List(ctx context.Context) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Metadata, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec.Metadata)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID > out[j].ID
	})
	return out, nil
}
