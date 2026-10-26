package offload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Store keeps full tool results. Put must not overwrite an existing key:
// refs are content-derived, so the first write is the right one.
//
// Keys are "<scope>:res_<hex>" for a full result and "<scope>:src:<handle>"
// for a src index entry (JSON {ref,path}). Implementations that expire or
// evict keys should give all keys the same TTL.
type Store interface {
	Put(ctx context.Context, key, value string) error
	Get(ctx context.Context, key string) (value string, ok bool, err error)
}

// Exister is an optional Store extension: Exists reports whether key is
// stored without fetching its value. Stores that hold large values
// should implement it.
type Exister interface {
	Exists(ctx context.Context, key string) (bool, error)
}

func storeKey(scope, name string) string { return scope + ":" + name }

// refFor is the content's ref: equal content, equal ref, across runs.
func refFor(content string) string {
	sum := sha256.Sum256([]byte(content))
	return "res_" + hex.EncodeToString(sum[:8])
}

// srcEntry is a src handle's index entry: where in which result it lives.
type srcEntry struct {
	Ref  string `json:"ref"`
	Path string `json:"path"`
}

// MemStore is an in-process Store for tests and single-process agents.
type MemStore struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemStore returns an empty in-process store.
func NewMemStore() *MemStore { return &MemStore{m: map[string]string{}} }

// Put stores value under key unless key already exists.
func (s *MemStore) Put(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[key]; !ok {
		s.m[key] = value
	}
	return nil
}

// Get returns the value stored under key.
func (s *MemStore) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

// Exists reports whether key is stored.
func (s *MemStore) Exists(_ context.Context, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[key]
	return ok, nil
}
