package taskmanager

import (
	"context"
	"sync"

	"github.com/farazhassan/gantry/task"
)

// InMemoryMetaStore is a process-local MetaStore backed by a map and mutex. It
// deep-copies on save and load so callers cannot mutate stored state by
// reference (mirrors task.InMemoryStore).
type InMemoryMetaStore struct {
	mu sync.Mutex
	m  map[string]*task.SessionMeta
}

// NewInMemoryMetaStore returns an empty in-memory MetaStore.
func NewInMemoryMetaStore() *InMemoryMetaStore {
	return &InMemoryMetaStore{m: make(map[string]*task.SessionMeta)}
}

// LoadMeta returns a deep copy of the session's meta, or ErrMetaNotFound.
func (s *InMemoryMetaStore) LoadMeta(_ context.Context, sessionID string) (*task.SessionMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.m[sessionID]
	if !ok {
		return nil, ErrMetaNotFound
	}
	return cloneMeta(m), nil
}

// SaveMeta stores a deep copy of m under the session id.
func (s *InMemoryMetaStore) SaveMeta(_ context.Context, sessionID string, m *task.SessionMeta) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sessionID] = cloneMeta(m)
	return nil
}

// cloneMeta returns a deep copy of m (nil-safe).
func cloneMeta(m *task.SessionMeta) *task.SessionMeta {
	if m == nil {
		return nil
	}
	cp := &task.SessionMeta{ActiveTaskID: m.ActiveTaskID}
	if m.TaskRefs != nil {
		cp.TaskRefs = make([]task.TaskRef, len(m.TaskRefs))
		copy(cp.TaskRefs, m.TaskRefs)
	}
	if m.Queue != nil {
		cp.Queue = make([]string, len(m.Queue))
		copy(cp.Queue, m.Queue)
	}
	return cp
}
