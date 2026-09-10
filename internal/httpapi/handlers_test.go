package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/badimirzai/image-service/internal/httpapi"
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

type testApp struct {
	handler http.Handler
	store   store.Store
}

func newTestApp(t *testing.T) testApp {
	t.Helper()
	st := store.NewMemoryStore()
	mux := http.NewServeMux()
	httpapi.NewServer(service.NewService(st)).RegisterRoutes(mux)
	return testApp{handler: mux, store: st}
}

func TestListImagesEmpty(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type=%q", ct)
	}

	var list []store.Metadata
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Fatalf("got %#v, want empty list", list)
	}
}

func TestCreateImageFormats(t *testing.T) {
	cases := []struct {
		name   string
		data   []byte
		format string
		w, h   int
	}{
		{"png", testPNG(t, 3, 2), "png", 3, 2},
		{"jpeg", testJPEG(t, 4, 4), "jpeg", 4, 4},
		{"gif", testGIF(t, 2, 3), "gif", 2, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			req := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(tc.data))
			rec := httptest.NewRecorder()
			app.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}

			var meta store.Metadata
			if err := json.NewDecoder(rec.Body).Decode(&meta); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if meta.ID != 1 {
				t.Fatalf("id=%d, want 1", meta.ID)
			}
			if loc := rec.Header().Get("Location"); loc != "/v1/images/1" {
				t.Fatalf("Location=%q, want /v1/images/1", loc)
			}
			if meta.ImageType != tc.format {
				t.Fatalf("image_type=%q, want %q", meta.ImageType, tc.format)
			}
			if meta.Width != tc.w || meta.Height != tc.h {
				t.Fatalf("dims=%dx%d, want %dx%d", meta.Width, meta.Height, tc.w, tc.h)
			}
			if meta.Filesize != int64(len(tc.data)) {
				t.Fatalf("filesize=%d, want %d", meta.Filesize, len(tc.data))
			}
			if meta.UploadDate == "" {
				t.Fatal("expected upload_date")
			}

			recStored, err := app.store.Get(context.Background(), meta.ID)
			if err != nil {
				t.Fatalf("store.Get: %v", err)
			}
			if !bytes.Equal(recStored.Bytes, tc.data) {
				t.Fatalf("stored bytes do not match uploaded payload")
			}
		})
	}
}

func TestCreateAndListImages(t *testing.T) {
	app := newTestApp(t)
	data := testPNG(t, 2, 2)

	req := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(data))
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created store.Metadata
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/v1/images", nil)
	listRec := httptest.NewRecorder()
	app.handler.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status=%d", listRec.Code)
	}

	var list []store.Metadata
	if err := json.NewDecoder(listRec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d items, want 1", len(list))
	}
	if list[0] != created {
		t.Fatalf("list metadata = %+v, want %+v", list[0], created)
	}
}

func TestCreateImageInvalid(t *testing.T) {
	app := newTestApp(t)

	t.Run("garbage", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader([]byte("nope")))
		rec := httptest.NewRecorder()
		app.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})

	t.Run("empty", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(nil))
		rec := httptest.NewRecorder()
		app.handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status=%d, want 400", rec.Code)
		}
	})
}

func TestCreateImageTooLarge(t *testing.T) {
	app := newTestApp(t)
	huge := bytes.NewReader(make([]byte, service.MaxImageSize+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/images", huge)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", rec.Code)
	}
}

func TestGetImageData(t *testing.T) {
	cases := []struct {
		name        string
		data        []byte
		contentType string
	}{
		{"png", testPNG(t, 3, 2), "image/png"},
		{"jpeg", testJPEG(t, 4, 4), "image/jpeg"},
		{"gif", testGIF(t, 2, 3), "image/gif"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newTestApp(t)

			postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(tc.data))
			postRec := httptest.NewRecorder()
			app.handler.ServeHTTP(postRec, postReq)
			if postRec.Code != http.StatusCreated {
				t.Fatalf("POST status=%d body=%s", postRec.Code, postRec.Body.String())
			}

			var meta store.Metadata
			if err := json.NewDecoder(postRec.Body).Decode(&meta); err != nil {
				t.Fatalf("decode create: %v", err)
			}

			getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/images/%d/data", meta.ID), nil)
			getRec := httptest.NewRecorder()
			app.handler.ServeHTTP(getRec, getReq)

			if getRec.Code != http.StatusOK {
				t.Fatalf("GET data status=%d body=%s", getRec.Code, getRec.Body.String())
			}
			if ct := getRec.Header().Get("Content-Type"); ct != tc.contentType {
				t.Fatalf("Content-Type=%q, want %q", ct, tc.contentType)
			}
			if cl := getRec.Header().Get("Content-Length"); cl != strconv.Itoa(len(tc.data)) {
				t.Fatalf("Content-Length=%q, want %d", cl, len(tc.data))
			}
			if !bytes.Equal(getRec.Body.Bytes(), tc.data) {
				t.Fatalf("body bytes do not match uploaded image")
			}
			// Must be raw bytes, not a JSON envelope.
			if bytes.HasPrefix(bytes.TrimSpace(getRec.Body.Bytes()), []byte("{")) {
				t.Fatalf("expected raw image bytes, got JSON-looking body")
			}
		})
	}
}

func TestGetImageDataNotFound(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/99/data", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

func TestGetImageDataInvalidID(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/abc/data", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}
