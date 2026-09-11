// Package service contains application logic: validation, metadata derivation,
// and orchestration. It depends on store.Store, not on a concrete engine.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"    // Added: enables gif.Encode
	_ "image/gif"  // register GIF decoder with image.DecodeConfig
	"image/jpeg"   // Added: enables jpeg.Encode
	_ "image/jpeg" // register JPEG decoder
	"image/png"    // Added: enables png.Encode
	_ "image/png"  // register PNG decoder
	"strconv"
	"strings"
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
	ErrInvalidBBox  = errors.New("invalid bbox") // handler maps this to 400
)

// BBox is a cutout rectangle in image pixel coordinates.
// Origin is top-left (0,0), same as Go's image package.
// W/H are width/height (not bottom-right corner).
type BBox struct {
	X, Y, W, H int
}

// ParseBBox parses "x,y,w,h" into a BBox.
// It only checks syntax and that w/h are positive - not image bounds.
// Bounds are checked later in GetImageCutout once we know width/height.
func ParseBBox(s string) (BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return BBox{}, fmt.Errorf("%w: want x,y,w,h", ErrInvalidBBox)
	}
	// Convert each field to int (allow spaces like "1, 2, 3, 4").
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			// Wrap ErrInvalidBBox so handlers can errors.Is -> 400.
			return BBox{}, fmt.Errorf("%w: %v", ErrInvalidBBox, err)
		}
		nums[i] = n
	}
	b := BBox{X: nums[0], Y: nums[1], W: nums[2], H: nums[3]}

	// Zero/negative size is never a valid cutout.
	if b.W <= 0 || b.H <= 0 {
		return BBox{}, fmt.Errorf("%w: width and height must be > 0", ErrInvalidBBox)
	}
	// Negative x/y (origin) also invalid even before we know image size.
	if b.X < 0 || b.Y < 0 {
		return BBox{}, fmt.Errorf("%w: x and y must be >= 0", ErrInvalidBBox)
	}
	return b, nil
}

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

// GetImageCutout returns a cropped re-encoding of the stored image for id.
// Read-time only: does not mutate the store; originals stay as uploaded.
// Flow: load bytes -> Decode -> validate bbox against dimensions -> SubImage -> Encode.
// Handler maps ErrNotFound -> 404, ErrInvalidBBox -> 400.
func (s *Service) GetImageCutout(ctx context.Context, id int64, bbox BBox) (store.ImageRecord, error) {
	// Same fetch as GetImageData; 404 if id missing.
	rec, err := s.store.Get(ctx, id)
	if err != nil {
		return store.ImageRecord{}, err
	}

	// Full Decode needed for pixels. Create/Update only use DecodeConfig (headers).
	img, format, err := image.Decode(bytes.NewReader(rec.Bytes))
	if err != nil {
		// Rare: we only persist validated JPEG/PNG/GIF on upload.
		return store.ImageRecord{}, fmt.Errorf("%w: %v", ErrInvalidImage, err)
	}

	// Strict in bounds: entire rectangle must fit inside the image.
	// Image coords: origin top-left, x->right, y->down (all non-negative).
	width, height := rec.Metadata.Width, rec.Metadata.Height
	if bbox.X+bbox.W > width || bbox.Y+bbox.H > height {
		return store.ImageRecord{}, fmt.Errorf(
			"%w: rectangle outside image (%dx%d)", ErrInvalidBBox, width, height,
		)
	}

	// image.Rect is half-open [Min, Max): Max = origin + size.
	rect := image.Rect(bbox.X, bbox.Y, bbox.X+bbox.W, bbox.Y+bbox.H)

	// SubImage is the cheap crop (shared backing pixels, new bounds).
	// Stdlib types from Decode (*YCbCr, *RGBA, *Paletted, …) support it.
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	si, ok := img.(subImager)
	if !ok {
		return store.ImageRecord{}, fmt.Errorf("%w: crop unsupported for type", ErrInvalidImage)
	}
	cropped := si.SubImage(rect)

	// Re-encode so the HTTP body is a real image file again (not raw pixels).
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, cropped, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&buf, cropped)
	case "gif":
		// Decode is first frame only for animated GIFs — fine for this assignment.
		err = gif.Encode(&buf, cropped, nil)
	default:
		return store.ImageRecord{}, fmt.Errorf("%w: format %q", ErrInvalidImage, format)
	}
	if err != nil {
		return store.ImageRecord{}, err
	}

	// Shape matches GetImageData so the handler can stay nearly identical.
	// Metadata here describes the *response cutout*, not a DB update.
	return store.ImageRecord{
		Metadata: store.Metadata{
			ID:        rec.Metadata.ID,
			Filesize:  int64(buf.Len()),
			Width:     bbox.W,
			Height:    bbox.H,
			ImageType: format, // keep same type as stored original
		},
		Bytes: buf.Bytes(),
	}, nil
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
// Missing ID -> store.ErrNotFound (no upsert). Keeps the original upload_date.
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
