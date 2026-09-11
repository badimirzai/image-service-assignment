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

func TestCreateImageRejectsTooLarge(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	data := make([]byte, service.MaxImageSize+1)
	_, err := svc.CreateImage(context.Background(), data)
	if !errors.Is(err, service.ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

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

func TestGetImageDataNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.GetImageData(context.Background(), 42)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

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

func TestGetImageMetadataNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.GetImageMetadata(context.Background(), 99)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

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

func TestUpdateImageNotFound(t *testing.T) {
	svc := service.NewService(store.NewMemoryStore())
	_, err := svc.UpdateImage(context.Background(), 99, testPNG(t, 1, 1))
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
