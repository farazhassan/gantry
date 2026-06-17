package qdrant

import (
	"context"
	"fmt"
	"strconv"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/embeddings"
)

const defaultTextKey = "content"

// Retriever embeds a query and returns the nearest stored documents. It
// satisfies retriever.Retriever.
type Retriever struct {
	store   *Store
	emb     embeddings.Embeddings
	textKey string
}

// RetrieverOption configures a Retriever.
type RetrieverOption func(*Retriever)

// WithTextKey sets the payload key holding document text (default "content").
// Ingest and retrieval must agree on this key.
func WithTextKey(key string) RetrieverOption {
	return func(r *Retriever) {
		if key != "" {
			r.textKey = key
		}
	}
}

// NewRetriever builds a Retriever from a Store and an Embeddings client.
func NewRetriever(store *Store, emb embeddings.Embeddings, opts ...RetrieverOption) *Retriever {
	r := &Retriever{store: store, emb: emb, textKey: defaultTextKey}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Retrieve embeds query, searches the store, and maps the k nearest hits to
// gantry.Document. The textKey payload entry becomes Content; the rest becomes
// Metadata.
func (r *Retriever) Retrieve(ctx context.Context, query string, k int) ([]gantry.Document, error) {
	if k <= 0 {
		return nil, nil
	}
	vecs, err := r.emb.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("qdrant: embed query: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("qdrant: embedder returned %d vectors for 1 query", len(vecs))
	}
	hits, err := r.store.Search(ctx, vecs[0], k)
	if err != nil {
		return nil, fmt.Errorf("qdrant: search: %w", err)
	}

	docs := make([]gantry.Document, len(hits))
	for i, h := range hits {
		content, _ := h.Payload[r.textKey].(string)
		meta := make(map[string]any, len(h.Payload))
		for key, v := range h.Payload {
			if key == r.textKey {
				continue
			}
			meta[key] = v
		}
		docs[i] = gantry.Document{
			ID:       strconv.FormatUint(h.ID, 10),
			Content:  content,
			Score:    h.Score,
			Metadata: meta,
		}
	}
	return docs, nil
}
