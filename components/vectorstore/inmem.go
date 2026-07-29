package vectorstore

import (
	"context"
	"math"
	"sort"
	"sync"
)

// InMemoryStore is a slice-backed Store using brute-force cosine similarity.
// Safe for concurrent use. Items whose vector length differs from the query
// (or with a zero-magnitude vector) score 0 rather than erroring.
type InMemoryStore struct {
	mu    sync.Mutex
	items []Item
}

// NewInMemoryStore returns an empty store.
func NewInMemoryStore() *InMemoryStore { return &InMemoryStore{} }

var _ Store = (*InMemoryStore)(nil)

func (s *InMemoryStore) Add(_ context.Context, items ...Item) error {
	s.mu.Lock()
	s.items = append(s.items, items...)
	s.mu.Unlock()
	return nil
}

func (s *InMemoryStore) Search(_ context.Context, vector []float32, k int) ([]Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	hits := make([]Hit, 0, len(s.items))
	for _, it := range s.items {
		hits = append(hits, Hit{Item: it, Score: cosine(vector, it.Vector)})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

// cosine returns the cosine similarity of a and b, or 0 when the lengths
// differ or either vector has zero magnitude.
func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
