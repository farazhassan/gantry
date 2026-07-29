package retriever

import (
	"context"
	"fmt"
	"strconv"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/embeddings"
	"github.com/farazhassan/gantry/components/vectorstore"
)

// VectorRetriever is a read-only Retriever over a vectorstore.Store: it embeds
// the query and returns the nearest stored items as Documents. It is the
// read-only counterpart to components/memory (which adds a persist loop over
// the same Store) — RAG without accumulating conversation.
type VectorRetriever struct {
	store vectorstore.Store
	emb   embeddings.Embeddings
}

var _ Retriever = (*VectorRetriever)(nil)

// NewVectorRetriever builds a Retriever from a vectorstore.Store and an
// Embeddings client. Seed the store out-of-band (e.g. with a knowledge base);
// Retrieve embeds each query and returns the k nearest items.
func NewVectorRetriever(store vectorstore.Store, emb embeddings.Embeddings) *VectorRetriever {
	return &VectorRetriever{store: store, emb: emb}
}

// Retrieve embeds query, searches the store, and maps the k nearest hits to
// gantry.Document (Text -> Content, Score -> Score, Metadata carried over).
// The store holds no stable IDs, so Document.ID is the rank index. A
// Document's Metadata may alias the store (see vectorstore.Store), so treat
// returned Documents as read-only.
func (r *VectorRetriever) Retrieve(ctx context.Context, query string, k int) ([]gantry.Document, error) {
	if k <= 0 {
		return nil, nil
	}
	vecs, err := r.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("retriever: embed query: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("retriever: embedder returned %d vectors for 1 query", len(vecs))
	}
	hits, err := r.store.Search(ctx, vecs[0], k)
	if err != nil {
		return nil, fmt.Errorf("retriever: search: %w", err)
	}
	docs := make([]gantry.Document, len(hits))
	for i, h := range hits {
		docs[i] = gantry.Document{
			ID:       strconv.Itoa(i),
			Content:  h.Text,
			Score:    h.Score,
			Metadata: h.Metadata,
		}
	}
	return docs, nil
}
