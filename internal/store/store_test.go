package store_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/badimirzai/image-service/internal/store"
)

// TestMemoryStoreCreateGetList covers ID assignment, defensive byte copy,
// Get round-trip, and newest-first List ordering.
func TestMemoryStoreCreateGetList(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	payload1 := []byte("abc")
	meta1, err := st.Create(ctx, store.Metadata{
		Filesize:  int64(len(payload1)),
		Width:     1,
		Height:    1,
		ImageType: "png",
	}, payload1)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta1.ID != 1 {
		t.Fatalf("got id %d, want 1", meta1.ID)
	}

	// Mutating the caller's slice must not change what was stored.
	payload1[0] = 'Z'

	got, err := st.Get(ctx, meta1.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.Bytes, []byte("abc")) {
		t.Fatalf("stored bytes = %q, want %q", got.Bytes, "abc")
	}
	if got.Metadata.ImageType != "png" {
		t.Fatalf("metadata = %+v", got.Metadata)
	}

	payload2 := []byte("x")
	meta2, err := st.Create(ctx, store.Metadata{
		Filesize:  1,
		Width:     2,
		Height:    2,
		ImageType: "jpeg",
	}, payload2)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if meta2.ID != 2 {
		t.Fatalf("got id %d, want 2", meta2.ID)
	}

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d items, want 2", len(list))
	}
	if list[0].ID != 2 || list[1].ID != 1 {
		t.Fatalf("expected newest-first order, got %+v", list)
	}
}

// TestMemoryStoreListEmpty ensures an empty store returns a zero-length list, not an error.
func TestMemoryStoreListEmpty(t *testing.T) {
	st := store.NewMemoryStore()
	list, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("got %d items, want 0", len(list))
	}
}

// TestMemoryStoreGetNotFound checks Get returns ErrNotFound for unknown ids.
func TestMemoryStoreGetNotFound(t *testing.T) {
	st := store.NewMemoryStore()
	_, err := st.Get(context.Background(), 99)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestMemoryStoreGetMetadata checks metadata-only fetch and not-found behavior.
func TestMemoryStoreGetMetadata(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()

	created, err := st.Create(ctx, store.Metadata{
		Filesize:  3,
		Width:     1,
		Height:    1,
		ImageType: "png",
	}, []byte("abc"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := st.GetMetadata(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetMetadata: %v", err)
	}
	if got != created {
		t.Fatalf("got %+v, want %+v", got, created)
	}

	_, err = st.GetMetadata(ctx, 99)
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}

// TestMemoryStoreConcurrentCreates stresses ID allocation under concurrent Creates
// (useful with go test -race).
func TestMemoryStoreConcurrentCreates(t *testing.T) {
	st := store.NewMemoryStore()
	ctx := context.Background()
	const n = 50

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, err := st.Create(ctx, store.Metadata{Filesize: 1, ImageType: "png"}, []byte{byte(i)})
			if err != nil {
				t.Errorf("Create: %v", err)
			}
		}(i)
	}
	wg.Wait()

	list, err := st.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != n {
		t.Fatalf("got %d items, want %d", len(list), n)
	}
	seen := make(map[int64]bool, n)
	for _, m := range list {
		if seen[m.ID] {
			t.Fatalf("duplicate id %d", m.ID)
		}
		seen[m.ID] = true
	}
}
