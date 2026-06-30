package checkpointer

import (
	"context"
	"sync"
)

// MemStore is a process-local, in-memory Store. Useful for tests; not suitable
// for resume across processes.
type MemStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

// NewMemStore returns an empty in-memory Store.
func NewMemStore() *MemStore { return &MemStore{blobs: map[string][]byte{}} }

func (s *MemStore) Put(_ context.Context, id string, blob []byte) error {
	cp := append([]byte(nil), blob...)
	s.mu.Lock()
	s.blobs[id] = cp
	s.mu.Unlock()
	return nil
}

func (s *MemStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[id]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), b...), true, nil
}

var _ Store = (*MemStore)(nil)

// NewInMemory returns a Checkpointer backed by an in-memory Store, persisting full
// State. Preserved for API compatibility (was *InMemoryCheckpointer).
func NewInMemory() *StoreCheckpointer {
	c, _ := FromStore(NewMemStore()) // cannot fail: non-nil store, no options
	return c
}
