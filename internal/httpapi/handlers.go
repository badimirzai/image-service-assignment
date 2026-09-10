// Package httpapi maps HTTP to the image service: routing, body limits,
// status codes, and headers. It does not decode or validate image formats.
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/badimirzai/image-service/internal/service"
	"github.com/badimirzai/image-service/internal/store"
	"strconv"
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

// handleGetImageData returns the raw image bytes for the given ID (not JSON).
func (s *Server) handleGetImageData(w http.ResponseWriter, r *http.Request) {
	idstr := r.PathValue("id")
	id, err := strconv.ParseInt(idstr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid image ID")
		return
	}
	record, err := s.svc.GetImageData(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "image not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	w.Header().Set("Content-Type", contentTypeFor(record.Metadata.ImageType))
	w.Header().Set("Content-Length", strconv.Itoa(len(record.Bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(record.Bytes)
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

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
