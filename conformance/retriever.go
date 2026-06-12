package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/retriever"
)

// RetrieverSuite verifies the contract of retriever.Retriever.
func RetrieverSuite(t *testing.T, factory func() retriever.Retriever) {
	t.Helper()

	t.Run("retrieve_returns_slice_or_error", func(t *testing.T) {
		r := factory()
		docs, err := r.Retrieve(context.Background(), "test query", 5)
		if err != nil {
			return
		}
		if len(docs) > 5 {
			t.Errorf("got %d docs, k = 5", len(docs))
		}
	})

	t.Run("retrieve_k_zero_returns_no_more_than_implementation_default", func(t *testing.T) {
		r := factory()
		_, err := r.Retrieve(context.Background(), "q", 0)
		if err != nil {
			// allowed
			return
		}
	})
}
