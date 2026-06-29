// Package retriever defines the Retriever interface and a static reference
// implementation. New returns a Component that installs PhaseAssembleContext middleware.
package retriever

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Retriever fetches relevant Documents for a query. Used for RAG.
type Retriever interface {
	Retrieve(ctx context.Context, query string, k int) ([]gantry.Document, error)
}
