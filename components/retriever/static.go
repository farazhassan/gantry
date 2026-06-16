package retriever

import (
	"context"

	"github.com/farazhassan/gantry"
)

// StaticRetriever returns the same fixed Documents for every query
// (truncated to k). Useful for tests and demos.
type StaticRetriever struct {
	docs []gantry.Document
}

// NewStatic returns a retriever that always returns the supplied docs.
func NewStatic(docs []gantry.Document) *StaticRetriever {
	out := make([]gantry.Document, len(docs))
	copy(out, docs)
	return &StaticRetriever{docs: out}
}

func (s *StaticRetriever) Retrieve(_ context.Context, _ string, k int) ([]gantry.Document, error) {
	if k <= 0 || k >= len(s.docs) {
		out := make([]gantry.Document, len(s.docs))
		copy(out, s.docs)
		return out, nil
	}
	out := make([]gantry.Document, k)
	copy(out, s.docs[:k])
	return out, nil
}
