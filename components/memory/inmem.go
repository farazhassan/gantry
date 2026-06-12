package memory

import (
	"context"
	"sync"

	"github.com/farazhassan/gantry/harness"
)

// InMemoryStore is a simple slice-backed Memory. Safe for concurrent use.
type InMemoryStore struct {
	mu       sync.Mutex
	messages []harness.Message
}

// NewInMemoryStore returns an empty store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{}
}

func (s *InMemoryStore) Append(_ context.Context, msg harness.Message) error {
	s.mu.Lock()
	s.messages = append(s.messages, msg)
	s.mu.Unlock()
	return nil
}

func (s *InMemoryStore) Read(_ context.Context) ([]harness.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]harness.Message, len(s.messages))
	copy(out, s.messages)
	return out, nil
}
