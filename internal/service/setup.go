package service

import "github.com/badimirzai/image-service/internal/store"

const MaxImageSize = 10 << 20 // 10 MiB limit per image
const MaxBatchSize = 20

// BatchItemError reports a single failed item within a batch upload.
type BatchItemError struct {
	Filename string `json:"filename"`
	Error    string `json:"error"`
}

// BatchResponse is the partial-success shape for batch uploads.
type BatchResponse struct {
	Success []store.Metadata `json:"success"`
	Errors  []BatchItemError `json:"errors"`
}

// Service holds application logic and depends on the Store interface.
type Service struct {
	store store.Store
}

func NewService(st store.Store) *Service {
	return &Service{store: st}
}
