package service_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/badimirzai/image-service/internal/service"
	"github.com/badimirzai/image-service/internal/store"
)

// testPNG / testJPEG / testGIF build tiny in-memory images so tests need no binary fixtures.
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func testJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	return buf.Bytes()
}

func testGIF(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("gif.Encode: %v", err)
	}
	return buf.Bytes()
}

// TestCreateImageFormats verifies create derives type/dims/filesize from bytes
// for JPEG, PNG, and GIF, and persists the original payload unchanged.
func TestCreateImageFormats(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		format string
		w, h   int
	}{
		{"png", testPNG(t, 8, 4), "png", 8, 4},
		{"jpeg", testJPEG(t, 6, 3), "jpeg", 6, 3},
		{"gif", testGIF(t, 5, 5), "gif", 5, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.NewMemoryStore()
			svc := service.NewService(st)
			ctx := context.Background()

			meta, err := svc.CreateImage(ctx, tc.data)
			if err != nil {
				t.Fatalf("CreateImage: %v", err)
			}
			if meta.ID != 1 {
				t.Fatalf("id=%d, want 1", meta.ID)
			}
			if meta.Width != tc.w || meta.Height != tc.h {
				t.Fatalf("dims=%dx%d, want %dx%d", meta.Width, meta.Height, tc.w, tc.h)
			}
			if meta.ImageType != tc.format {
				t.Fatalf("type=%q, want %q", meta.ImageType, tc.format)
			}
			if meta.Filesize != int64(len(tc.data)) {
				t.Fatalf("filesize=%d, want %d", meta.Filesize, len(tc.data))
			}
			if meta.UploadDate == "" {
				t.Fatal("expected upload_date")
			}

			// Bytes must be stored unchanged (via store boundary, not HTTP).
			rec, err := st.Get(ctx, meta.ID)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if !bytes.Equal(rec.Bytes, tc.data) {
				t.Fatalf("stored bytes differ from upload (len got %d want %d)", len(rec.Bytes), len(tc.data))
			}
		})
	}
}

// TestCreateImageAndList checks that a created image appears in ListImages.
func TestCreateImageAndList(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	data := testPNG(t, 8, 4)

	meta, err := svc.CreateImage(ctx, data)
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	list, err := svc.ListImages(ctx)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(list) != 1 || list[0].ID != meta.ID {
		t.Fatalf("unexpected list: %+v", list)
	}
	if list[0].ImageType != "png" || list[0].Width != 8 {
		t.Fatalf("list metadata mismatch: %+v", list[0])
	}
}

// TestCreateImageRejectsInvalid covers empty body and undecodable garbage → sentinel errors.
func TestCreateImageRejectsInvalid(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())

	_, err := svc.CreateImage(context.Background(), nil)
	if !errors.Is(err, service.ErrEmptyImage) {
		t.Fatalf("empty: got %v, want ErrEmptyImage", err)
	}

	_, err = svc.CreateImage(context.Background(), []byte{})
	if !errors.Is(err, service.ErrEmptyImage) {
		t.Fatalf("empty slice: got %v, want ErrEmptyImage", err)
	}

	_, err = svc.CreateImage(context.Background(), []byte("not-an-image"))
	if !errors.Is(err, service.ErrInvalidImage) {
		t.Fatalf("garbage: got %v, want ErrInvalidImage", err)
	}
}

// TestCreateImageRejectsTooLarge checks the service-level size limit (beyond HTTP MaxBytesReader).
func TestCreateImageRejectsTooLarge(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	data := make([]byte, service.MaxImageSize+1)
	_, err := svc.CreateImage(context.Background(), data)
	if !errors.Is(err, service.ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

// TestListImagesEmpty ensures empty store → non-nil empty slice (JSON []).
func TestListImagesEmpty(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	list, err := svc.ListImages(context.Background())
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("got %#v, want empty non-nil slice", list)
	}
}

// TestGetImageData returns original bytes + metadata for a known id.
func TestGetImageData(t *testing.T) {
	st := store.NewMemoryStore()
	svc := service.NewService(st)
	ctx := context.Background()
	data := testPNG(t, 5, 5)

	meta, err := svc.CreateImage(ctx, data)
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	rec, err := svc.GetImageData(ctx, meta.ID)
	if err != nil {
		t.Fatalf("GetImageData: %v", err)
	}
	if !bytes.Equal(rec.Bytes, data) {
		t.Fatalf("bytes mismatch")
	}
	if rec.Metadata.ImageType != "png" || rec.Metadata.ID != meta.ID {
		t.Fatalf("unexpected metadata: %+v", rec.Metadata)
	}
}

// TestGetImageDataNotFound checks missing id → ErrNotFound.
func TestGetImageDataNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.GetImageData(context.Background(), 42)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestGetImageMetadata returns the same metadata produced at create time.
func TestGetImageMetadata(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	data := testPNG(t, 4, 4)

	created, err := svc.CreateImage(ctx, data)
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	got, err := svc.GetImageMetadata(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetImageMetadata: %v", err)
	}
	if got != created {
		t.Fatalf("got %+v, want %+v", got, created)
	}
}

// TestGetImageMetadataNotFound checks missing id → ErrNotFound.
func TestGetImageMetadataNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.GetImageMetadata(context.Background(), 99)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestUpdateImage replaces bytes/type/dims, preserves id + upload_date (no upsert semantics here).
func TestUpdateImage(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	original := testPNG(t, 2, 2)
	updated := testJPEG(t, 5, 3)

	created, err := svc.CreateImage(ctx, original)
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	meta, err := svc.UpdateImage(ctx, created.ID, updated)
	if err != nil {
		t.Fatalf("UpdateImage: %v", err)
	}
	if meta.ImageType != "jpeg" || meta.Width != 5 || meta.Height != 3 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if meta.UploadDate != created.UploadDate {
		t.Fatalf("upload_date should be preserved")
	}

	rec, err := svc.GetImageData(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetImageData: %v", err)
	}
	if !bytes.Equal(rec.Bytes, updated) {
		t.Fatalf("bytes not updated")
	}
}

// TestUpdateImageInvalid rejects non-image replacement payloads.
func TestUpdateImageInvalid(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	created, err := svc.CreateImage(ctx, testPNG(t, 2, 2))
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	_, err = svc.UpdateImage(ctx, created.ID, []byte("nope"))
	if !errors.Is(err, service.ErrInvalidImage) {
		t.Fatalf("got %v, want ErrInvalidImage", err)
	}
}

// TestUpdateImageNotFound ensures PUT does not upsert missing ids.
func TestUpdateImageNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.UpdateImage(context.Background(), 99, testPNG(t, 1, 1))
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestParseBBox covers happy path, whitespace, and invalid x,y,w,h forms.
func TestParseBBox(t *testing.T) {
	ok, err := service.ParseBBox("1,2,3,4")
	if err != nil {
		t.Fatalf("ParseBBox: %v", err)
	}
	if ok != (service.BBox{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("got %+v", ok)
	}

	spaced, err := service.ParseBBox(" 0 , 0 , 10 , 8 ")
	if err != nil || spaced.W != 10 || spaced.H != 8 {
		t.Fatalf("spaced: %+v err=%v", spaced, err)
	}

	// Wrong arity, non-ints, zero/negative size, negative origin.
	cases := []string{"1,2,3", "a,b,c,d", "0,0,0,1", "0,0,1,0", "-1,0,1,1", "0,-1,1,1"}
	for _, s := range cases {
		if _, err := service.ParseBBox(s); !errors.Is(err, service.ErrInvalidBBox) {
			t.Fatalf("%q: got %v, want ErrInvalidBBox", s, err)
		}
	}
}

// TestGetImageCutout crops to expected dims, re-encodes as the same format,
// and leaves the stored original untouched (read-time only).
func TestGetImageCutout(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	data := testPNG(t, 10, 8)

	created, err := svc.CreateImage(ctx, data)
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}

	cut, err := svc.GetImageCutout(ctx, created.ID, service.BBox{X: 2, Y: 1, W: 4, H: 3})
	if err != nil {
		t.Fatalf("GetImageCutout: %v", err)
	}
	if cut.Metadata.Width != 4 || cut.Metadata.Height != 3 {
		t.Fatalf("cutout meta dims=%dx%d", cut.Metadata.Width, cut.Metadata.Height)
	}
	if cut.Metadata.ImageType != "png" {
		t.Fatalf("type=%q", cut.Metadata.ImageType)
	}

	decoded, format, err := image.Decode(bytes.NewReader(cut.Bytes))
	if err != nil {
		t.Fatalf("decode cutout: %v", err)
	}
	if format != "png" {
		t.Fatalf("format=%q", format)
	}
	if decoded.Bounds().Dx() != 4 || decoded.Bounds().Dy() != 3 {
		t.Fatalf("decoded dims=%v", decoded.Bounds())
	}

	// Original in store must be unchanged.
	orig, err := svc.GetImageData(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetImageData: %v", err)
	}
	if !bytes.Equal(orig.Bytes, data) {
		t.Fatal("cutout mutated stored original")
	}
}

// TestGetImageCutoutOutOfBounds: strict in-bounds policy (3+3>5 on a 5x5 image).
func TestGetImageCutoutOutOfBounds(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	ctx := context.Background()
	created, err := svc.CreateImage(ctx, testPNG(t, 5, 5))
	if err != nil {
		t.Fatalf("CreateImage: %v", err)
	}
	_, err = svc.GetImageCutout(ctx, created.ID, service.BBox{X: 3, Y: 3, W: 3, H: 3})
	if !errors.Is(err, service.ErrInvalidBBox) {
		t.Fatalf("got %v, want ErrInvalidBBox", err)
	}
}

// TestGetImageCutoutNotFound checks missing id before crop work matters.
func TestGetImageCutoutNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.GetImageCutout(context.Background(), 99, service.BBox{X: 0, Y: 0, W: 1, H: 1})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
