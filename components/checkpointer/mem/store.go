package mem

import (
	"context"
	"sync"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// Store is a process-local, in-memory Store. Useful for tests; not suitable
// for resume across processes.
type Store struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

// NewStore returns an empty in-memory Store.
func NewStore() *Store { return &Store{blobs: map[string][]byte{}} }

func (s *Store) Put(_ context.Context, id string, blob []byte) error {
	cp := append([]byte(nil), blob...)
	s.mu.Lock()
	s.blobs[id] = cp
	s.mu.Unlock()
	return nil
}

func (s *Store) Get(_ context.Context, id string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[id]
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), b...), true, nil
}

var _ checkpointer.Store = (*Store)(nil)

// New returns a Checkpointer backed by an in-memory Store, persisting full
// State.
func New() *checkpointer.StoreCheckpointer {
	c, _ := checkpointer.FromStore(NewStore()) // cannot fail: non-nil store, no options
	return c
}
