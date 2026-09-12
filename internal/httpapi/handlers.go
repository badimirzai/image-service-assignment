// Package httpapi maps HTTP to the image service: routing, body limits,
// status codes, and headers. It does not decode or validate image formats.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/badimirzai/image-service/internal/service"
	"github.com/badimirzai/image-service/internal/store"
)

// Server wires HTTP handlers to the image service.
type Server struct {
	svc *service.Service
}

// NewServer returns an HTTP API bound to svc.
func NewServer(svc *service.Service) *Server {
	return &Server{svc: svc}
}

// RegisterRoutes mounts HTTP routes on mux (Go 1.22+ method-aware patterns).
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/images", s.handleListImages)
	mux.HandleFunc("POST /v1/images", s.handleCreateImage)
	mux.HandleFunc("GET /v1/images/{id}/data", s.handleGetImageData)
	mux.HandleFunc("GET /v1/images/{id}", s.handleGetImageMetadata)
	mux.HandleFunc("PUT /v1/images/{id}", s.handleUpdateImage)
	mux.HandleFunc("POST /v1/images/batch", s.handleCreateBatch)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListImages returns metadata for all images (200 + JSON array).
func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	list, err := s.svc.ListImages(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleGetImageData returns raw image bytes for id (not JSON).
// Optional ?bbox=x,y,w,h -> cutout via GetImageCutout (storage unchanged).
func (s *Server) handleGetImageData(w http.ResponseWriter, r *http.Request) {
	idstr := r.PathValue("id")
	id, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image ID")
		return
	}

	// Absent or empty bbox -> original bytes path (unchanged behavior).
	bboxStr := r.URL.Query().Get("bbox")

	var record store.ImageRecord
	if bboxStr == "" {
		record, err = s.svc.GetImageData(r.Context(), id)
	} else {
		// Syntax / basic numeric checks in service; bounds checked in GetImageCutout.
		bbox, perr := service.ParseBBox(bboxStr)
		if perr != nil {
			writeError(w, http.StatusBadRequest, perr.Error())
			return
		}
		record, err = s.svc.GetImageCutout(r.Context(), id, bbox)
	}
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "image not found")
		case errors.Is(err, service.ErrInvalidBBox):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	w.Header().Set("Content-Type", contentTypeFor(record.Metadata.ImageType))
	w.Header().Set("Content-Length", strconv.Itoa(len(record.Bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(record.Bytes)
}

// handleGetImageMetadata returns JSON metadata for one image (200).
func (s *Server) handleGetImageMetadata(w http.ResponseWriter, r *http.Request) {
	idstr := r.PathValue("id")
	id, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image ID")
		return
	}
	metadata, err := s.svc.GetImageMetadata(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "image not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

// helper function to get the content type for the image type
func contentTypeFor(imageType string) string {
	switch imageType {
	case "jpeg":
		return "image/jpeg"
	case "png":
		return "image/png"
	case "gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}

// handleUpdateImage replaces an existing image's bytes and derived metadata (PUT).
// Does not upsert: missing ID -> 404. Body is raw image bytes (same as POST).
func (s *Server) handleUpdateImage(w http.ResponseWriter, r *http.Request) {
	idstr := r.PathValue("id")
	id, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image ID")
		return
	}

	// Cap body size at the transport edge using the same limit as the service.
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxImageSize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("image exceeds size limit: %d bytes", maxErr.Limit))
			return
		}
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	metadata, err := s.svc.UpdateImage(r.Context(), id, data)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "image not found")
		case errors.Is(err, service.ErrEmptyImage), errors.Is(err, service.ErrInvalidImage):
			// Unsupported/invalid formats surface here as 400 (same as POST).
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	writeJSON(w, http.StatusOK, metadata)
}

// handleCreateImage accepts raw image bytes in the request body
// (e.g. curl --data-binary @image.jpg). Content-Type is not trusted.
func (s *Server) handleCreateImage(w http.ResponseWriter, r *http.Request) {
	// Cap body size at the transport edge using the same limit as the service.
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxImageSize)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("image exceeds size limit: %d bytes", maxErr.Limit))
			return
		}
		writeError(w, http.StatusBadRequest, "failed to read body")
		return
	}

	meta, err := s.svc.CreateImage(r.Context(), data)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmptyImage), errors.Is(err, service.ErrInvalidImage):
			writeError(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, service.ErrTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	// 201 Created with Location of the new resource (get-by-id comes later).
	w.Header().Set("Location", fmt.Sprintf("/v1/images/%d", meta.ID))
	writeJSON(w, http.StatusCreated, meta)
}

// handleCreateBatch accepts multipart/form-data with one or more file parts
// named "images". Parses/limits in the handler; CreateBatch does validation
// and bounded-concurrent create. Always 200 with partial-success JSON when
// the batch is accepted for processing.
func (s *Server) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(ct, "multipart/form-data") {
		writeError(w, http.StatusUnsupportedMediaType, "expected multipart/form-data")
		return
	}

	// Cap the whole request roughly to max batch * max image size.
	maxBody := int64(service.MaxBatchSize) * service.MaxImageSize
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)

	// Keep some parts in memory; larger spill to temp files.
	const maxMemory = 32 << 20 // 32 MiB
	if err := r.ParseMultipartForm(maxMemory); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds size limit")
			return
		}
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}
	defer r.MultipartForm.RemoveAll()

	files := r.MultipartForm.File["images"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, service.ErrEmptyBatch.Error())
		return
	}
	if len(files) > service.MaxBatchSize {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("%s: limit is %d", service.ErrBatchTooManyImages.Error(), service.MaxBatchSize))
		return
	}

	items := make([]service.BatchItem, 0, len(files))
	var partErrors []service.BatchItemError
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read multipart file")
			return
		}
		// Read at most MaxImageSize+1 to detect oversized parts without trusting fh.Size.
		data, err := io.ReadAll(io.LimitReader(f, service.MaxImageSize+1))
		_ = f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, "failed to read multipart file")
			return
		}
		name := fh.Filename
		if name == "" {
			name = "unnamed"
		}
		// Oversized part → per-item error (partial success), not 413 for the whole batch.
		if int64(len(data)) > service.MaxImageSize {
			partErrors = append(partErrors, service.BatchItemError{
				Filename: name,
				Error:    service.ErrTooLarge.Error(),
			})
			continue
		}
		items = append(items, service.BatchItem{
			Filename: fh.Filename,
			Data:     data,
		})
	}

	resp := service.BatchResponse{
		Success: []store.Metadata{},
		Errors:  partErrors,
	}
	if len(items) > 0 {
		created, err := s.svc.CreateBatch(r.Context(), items)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrEmptyBatch), errors.Is(err, service.ErrBatchTooManyImages):
				writeError(w, http.StatusBadRequest, err.Error())
			default:
				writeError(w, http.StatusInternalServerError, "internal error")
			}
			return
		}
		resp.Success = created.Success
		resp.Errors = append(resp.Errors, created.Errors...)
	}

	writeJSON(w, http.StatusOK, resp)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
