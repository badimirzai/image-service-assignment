// Package service contains application logic: validation, metadata derivation,
// and orchestration. It depends on store.Store, not on a concrete engine.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder with image.DecodeConfig
	_ "image/jpeg" // register JPEG decoder
	_ "image/png"  // register PNG decoder
	"time"

	"github.com/badimirzai/image-service/internal/store"
)

const MaxImageSize = 10 << 20 // 10 MiB limit per image (also enforced at HTTP edge)
const MaxBatchSize = 20       // reserved for batch upload (not wired yet)

// Sentinel errors mapped to HTTP status codes by the handler layer.
var (
	ErrEmptyImage   = errors.New("empty image body")
	ErrInvalidImage = errors.New("invalid or unsupported image")
	ErrTooLarge     = errors.New("image exceeds size limit")
)

// BatchItemError reports a single failed item within a batch upload.
type BatchItemError struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// BatchResponse is the partial-success shape for batch uploads (not wired yet).
type BatchResponse struct {
	Success []store.Metadata `json:"success"`
	Errors  []BatchItemError `json:"errors"`
}

// Service holds application logic and depends on the Store interface only.
type Service struct {
	store store.Store
}

// NewService wires application logic to a Store implementation.
func NewService(st store.Store) *Service {
	return &Service{store: st}
}

// ListImages returns metadata for all stored images.
// An empty store yields an empty JSON array, never null.
func (s *Service) ListImages(ctx context.Context) ([]store.Metadata, error) {
	list, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		return []store.Metadata{}, nil
	}
	return list, nil
}

// GetImageData returns the stored image record for id (metadata + original bytes).
func (s *Service) GetImageData(ctx context.Context, id int64) (store.ImageRecord, error) {
	record, err := s.store.Get(ctx, id)
	if err != nil {
		return store.ImageRecord{}, err
	}
	return record, nil
}

// GetImageMetadata returns metadata for the image with the given id.
func (s *Service) GetImageMetadata(ctx context.Context, id int64) (store.Metadata, error) {
	return s.store.GetMetadata(ctx, id)
}

// CreateImage validates that data is a supported image, derives metadata from
// the bytes themselves (not from Content-Type), and stores the original payload
// unchanged. Supported formats: JPEG, PNG, GIF.
func (s *Service) CreateImage(ctx context.Context, data []byte) (store.Metadata, error) {
	if len(data) == 0 {
		return store.Metadata{}, ErrEmptyImage
	}
	if len(data) > MaxImageSize {
		return store.Metadata{}, ErrTooLarge
	}

	// DecodeConfig reads headers only; format/dimensions come from the payload.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return store.Metadata{}, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}
	if format != "jpeg" && format != "png" && format != "gif" {
		return store.Metadata{}, fmt.Errorf("%w: format %q", ErrInvalidImage, format)
	}

	meta := store.Metadata{
		Filesize:   int64(len(data)),
		Width:      cfg.Width,
		Height:     cfg.Height,
		ImageType:  format,
		UploadDate: time.Now().UTC().Format(time.RFC3339),
		// Filename left empty for raw-body uploads; batch may set it later.
	}

	return s.store.Create(ctx, meta, data)
}

// UpdateImage replaces an existing image: validates bytes (JPEG/PNG/GIF), derives
// new metadata from the payload, and stores the original bytes unchanged.
// Missing ID → store.ErrNotFound (no upsert). Keeps the original upload_date.
func (s *Service) UpdateImage(ctx context.Context, id int64, data []byte) (store.Metadata, error) {
	if len(data) == 0 {
		return store.Metadata{}, ErrEmptyImage
	}
	if len(data) > MaxImageSize {
		return store.Metadata{}, ErrTooLarge
	}

	// DecodeConfig reads headers only; format/dimensions come from the payload.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return store.Metadata{}, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}
	if format != "jpeg" && format != "png" && format != "gif" {
		return store.Metadata{}, fmt.Errorf("%w: format %q", ErrInvalidImage, format)
	}

	existing, err := s.store.GetMetadata(ctx, id)
	if err != nil {
		return store.Metadata{}, err
	}

	meta := existing
	meta.Filesize = int64(len(data))
	meta.Width = cfg.Width
	meta.Height = cfg.Height
	meta.ImageType = format
	// UploadDate intentionally preserved from the original create.

	return s.store.Update(ctx, id, meta, data)
}
