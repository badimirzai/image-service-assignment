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
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/badimirzai/image-service/internal/httpapi"
	"github.com/badimirzai/image-service/internal/service"
	"github.com/badimirzai/image-service/internal/store"
)

// Tiny generated fixtures — no binary image files in the repo
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

// testApp wires memory store → service → mux like cmd/server, for httptest tests.
type testApp struct {
	handler http.Handler
	store   store.Store // exposed so tests can assert persisted bytes via the store boundary
}

func newTestApp(t *testing.T) testApp {
	t.Helper()
	st := store.NewMemoryStore()
	mux := http.NewServeMux()
	httpapi.NewServer(service.NewService(st)).RegisterRoutes(mux)
	return testApp{handler: mux, store: st}
}

// TestListImagesEmpty: GET /v1/images on empty store → 200 + [].
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

// TestCreateImageFormats: POST raw body for each supported format → 201, Location, metadata, stored bytes.
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

// TestCreateAndListImages: POST then GET list returns the created metadata.
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

// TestCreateImageInvalid: garbage / empty body → 400.
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

// TestCreateImageTooLarge: body over MaxImageSize → 413 at the HTTP edge.
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

// TestGetImageData: GET /data returns raw image bytes (not JSON) with correct Content-Type/Length.
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

// TestGetImageDataNotFound: unknown id → 404.
func TestGetImageDataNotFound(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/99/data", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// TestGetImageDataInvalidID: non-integer path id → 400.
func TestGetImageDataInvalidID(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/abc/data", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

// TestGetImageMetadata: GET /v1/images/{id} returns JSON metadata matching create.
func TestGetImageMetadata(t *testing.T) {
	app := newTestApp(t)
	data := testPNG(t, 3, 2)

	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(data))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d body=%s", postRec.Code, postRec.Body.String())
	}

	var created store.Metadata
	if err := json.NewDecoder(postRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/images/%d", created.ID), nil)
	getRec := httptest.NewRecorder()
	app.handler.ServeHTTP(getRec, getReq)

	if getRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if ct := getRec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type=%q", ct)
	}

	var got store.Metadata
	if err := json.NewDecoder(getRec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != created {
		t.Fatalf("got %+v, want %+v", got, created)
	}
}

// TestGetImageMetadataNotFound: unknown id → 404.
func TestGetImageMetadataNotFound(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/99", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// TestGetImageMetadataInvalidID: non-integer path id → 400.
func TestGetImageMetadataInvalidID(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/abc", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", rec.Code)
	}
}

// TestUpdateImage: PUT replaces payload/metadata, keeps upload_date, updates /data bytes.
func TestUpdateImage(t *testing.T) {
	app := newTestApp(t)
	original := testPNG(t, 2, 2)
	updated := testJPEG(t, 4, 4)

	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(original))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d", postRec.Code)
	}
	var created store.Metadata
	if err := json.NewDecoder(postRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	putReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/v1/images/%d", created.ID), bytes.NewReader(updated))
	putRec := httptest.NewRecorder()
	app.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	var meta store.Metadata
	if err := json.NewDecoder(putRec.Body).Decode(&meta); err != nil {
		t.Fatalf("decode put: %v", err)
	}
	if meta.ID != created.ID {
		t.Fatalf("id=%d, want %d", meta.ID, created.ID)
	}
	if meta.ImageType != "jpeg" || meta.Width != 4 || meta.Height != 4 {
		t.Fatalf("unexpected metadata after PUT: %+v", meta)
	}
	if meta.UploadDate != created.UploadDate {
		t.Fatalf("upload_date changed on PUT: got %q want %q", meta.UploadDate, created.UploadDate)
	}
	if meta.Filesize != int64(len(updated)) {
		t.Fatalf("filesize=%d, want %d", meta.Filesize, len(updated))
	}

	dataReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/v1/images/%d/data", created.ID), nil)
	dataRec := httptest.NewRecorder()
	app.handler.ServeHTTP(dataRec, dataReq)
	if !bytes.Equal(dataRec.Body.Bytes(), updated) {
		t.Fatalf("stored bytes were not replaced")
	}
}

// TestUpdateImageInvalidFormat: unsupported replacement body → 400 with clear error text.
func TestUpdateImageInvalidFormat(t *testing.T) {
	app := newTestApp(t)
	original := testPNG(t, 2, 2)

	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(original))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d", postRec.Code)
	}

	putReq := httptest.NewRequest(http.MethodPut, "/v1/images/1", bytes.NewReader([]byte("not-an-image")))
	putRec := httptest.NewRecorder()
	app.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", putRec.Code, putRec.Body.String())
	}
	if !bytes.Contains(putRec.Body.Bytes(), []byte("invalid or unsupported image")) {
		t.Fatalf("expected invalid image error message, got %s", putRec.Body.String())
	}
}

// TestUpdateImageNotFound: PUT to missing id → 404 (no upsert).
func TestUpdateImageNotFound(t *testing.T) {
	app := newTestApp(t)
	data := testPNG(t, 2, 2)
	putReq := httptest.NewRequest(http.MethodPut, "/v1/images/99", bytes.NewReader(data))
	putRec := httptest.NewRecorder()
	app.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", putRec.Code)
	}
}

// TestUpdateImageTooLarge: oversized PUT body → 413.
func TestUpdateImageTooLarge(t *testing.T) {
	app := newTestApp(t)
	original := testPNG(t, 2, 2)
	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(original))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d", postRec.Code)
	}

	huge := bytes.NewReader(make([]byte, service.MaxImageSize+1))
	putReq := httptest.NewRequest(http.MethodPut, "/v1/images/1", huge)
	putRec := httptest.NewRecorder()
	app.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d, want 413", putRec.Code)
	}
}

// TestUpdateImageInvalidID: non-integer path id → 400.
func TestUpdateImageInvalidID(t *testing.T) {
	app := newTestApp(t)
	putReq := httptest.NewRequest(http.MethodPut, "/v1/images/abc", bytes.NewReader(testPNG(t, 1, 1)))
	putRec := httptest.NewRecorder()
	app.handler.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", putRec.Code)
	}
}

// TestGetImageDataBBox: ?bbox=x,y,w,h returns a cropped image; storage stays unchanged.
func TestGetImageDataBBox(t *testing.T) {
	app := newTestApp(t)
	data := testPNG(t, 10, 8)

	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(data))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d", postRec.Code)
	}

	// bbox=1,2,3,4 → response should decode as 3×4 PNG.
	getReq := httptest.NewRequest(http.MethodGet, "/v1/images/1/data?bbox=1,2,3,4", nil)
	getRec := httptest.NewRecorder()
	app.handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	if ct := getRec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type=%q", ct)
	}

	img, format, err := image.Decode(getRec.Body)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if format != "png" {
		t.Fatalf("format=%q", format)
	}
	if img.Bounds().Dx() != 3 || img.Bounds().Dy() != 4 {
		t.Fatalf("dims=%v, want 3x4", img.Bounds())
	}

	// Storage unchanged: full fetch still returns original bytes.
	fullReq := httptest.NewRequest(http.MethodGet, "/v1/images/1/data", nil)
	fullRec := httptest.NewRecorder()
	app.handler.ServeHTTP(fullRec, fullReq)
	if !bytes.Equal(fullRec.Body.Bytes(), data) {
		t.Fatal("bbox request mutated stored original")
	}
}

// TestGetImageDataBBoxBadRequest: malformed / OOB / zero-size bbox → 400.
func TestGetImageDataBBoxBadRequest(t *testing.T) {
	app := newTestApp(t)
	postReq := httptest.NewRequest(http.MethodPost, "/v1/images", bytes.NewReader(testPNG(t, 5, 5)))
	postRec := httptest.NewRecorder()
	app.handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST status=%d", postRec.Code)
	}

	cases := []struct {
		name string
		url  string
	}{
		{"malformed", "/v1/images/1/data?bbox=1,2"},
		{"out_of_bounds", "/v1/images/1/data?bbox=0,0,10,10"},
		{"zero_size", "/v1/images/1/data?bbox=0,0,0,1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			app.handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestGetImageDataBBoxNotFound: bbox on missing id → 404 (not 400).
func TestGetImageDataBBoxNotFound(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/images/99/data?bbox=0,0,1,1", nil)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", rec.Code)
	}
}

// multipartBody builds a multipart/form-data body with file parts named "images".
func multipartBody(t *testing.T, files map[string][]byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, data := range files {
		part, err := w.CreateFormFile("images", name)
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// TestCreateBatch: mixed valid/invalid parts → 200 with partial success.
func TestCreateBatch(t *testing.T) {
	app := newTestApp(t)
	body, ct := multipartBody(t, map[string][]byte{
		"a.png":   testPNG(t, 2, 2),
		"bad.bin": []byte("not-an-image"),
		"b.jpg":   testJPEG(t, 3, 3),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/batch", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp service.BatchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Success) != 2 {
		t.Fatalf("success=%d, want 2; resp=%+v", len(resp.Success), resp)
	}
	if len(resp.Errors) != 1 {
		t.Fatalf("errors=%d, want 1; resp=%+v", len(resp.Errors), resp)
	}
}

// TestCreateBatchNotMultipart: wrong Content-Type → 415.
func TestCreateBatchNotMultipart(t *testing.T) {
	app := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/images/batch", bytes.NewReader([]byte("x")))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status=%d, want 415", rec.Code)
	}
}

// TestCreateBatchEmpty: multipart with no "images" parts → 400.
func TestCreateBatchEmpty(t *testing.T) {
	app := newTestApp(t)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("note", "no files")
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/batch", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

// TestCreateBatchTooMany: more than MaxBatchSize parts → 400.
func TestCreateBatchTooMany(t *testing.T) {
	app := newTestApp(t)
	files := make(map[string][]byte, service.MaxBatchSize+1)
	png := testPNG(t, 1, 1)
	for i := 0; i < service.MaxBatchSize+1; i++ {
		files[fmt.Sprintf("%d.png", i)] = png
	}
	body, ct := multipartBody(t, files)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/batch", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s, want 400", rec.Code, rec.Body.String())
	}
}

// TestCreateBatchOversizedPart: one part over MaxImageSize is a per-item error; others still succeed (200).
func TestCreateBatchOversizedPart(t *testing.T) {
	app := newTestApp(t)
	body, ct := multipartBody(t, map[string][]byte{
		"ok.png":   testPNG(t, 2, 2),
		"huge.bin": make([]byte, service.MaxImageSize+1),
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/batch", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	app.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s, want 200", rec.Code, rec.Body.String())
	}
	var resp service.BatchResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Success) != 1 {
		t.Fatalf("success=%d, want 1; %+v", len(resp.Success), resp)
	}
	if len(resp.Errors) != 1 || resp.Errors[0].Filename != "huge.bin" {
		t.Fatalf("errors=%+v", resp.Errors)
	}
	if !strings.Contains(resp.Errors[0].Error, "size limit") {
		t.Fatalf("expected size-limit error, got %q", resp.Errors[0].Error)
	}
}
