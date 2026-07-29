package retriever_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/components/vectorstore"
)

// stubEmbedder returns canned vectors by exact text, defaulting to {0, 0}.
type stubEmbedder struct{ vecs map[string][]float32 }

func (e stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := e.vecs[t]; ok {
			out[i] = v
		} else {
			out[i] = []float32{0, 0}
		}
	}
	return out, nil
}

func TestVectorRetrieverRanksAndMapsHits(t *testing.T) {
	ctx := context.Background()
	emb := stubEmbedder{vecs: map[string][]float32{
		"east":  {1, 0},
		"north": {0, 1},
		"query": {1, 0}, // aligned with "east"
	}}
	store := vectorstore.NewInMemoryStore()
	if err := store.Add(ctx,
		vectorstore.Item{Text: "east", Vector: []float32{1, 0}, Metadata: map[string]any{"dir": "e"}},
		vectorstore.Item{Text: "north", Vector: []float32{0, 1}},
	); err != nil {
		t.Fatalf("Add: %v", err)
	}

	r := retriever.NewVectorRetriever(store, emb)
	docs, err := r.Retrieve(ctx, "query", 1)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d docs, want 1", len(docs))
	}
	if docs[0].Content != "east" {
		t.Errorf("Content = %q, want %q", docs[0].Content, "east")
	}
	if docs[0].Score < 0.99 {
		t.Errorf("Score = %v, want ~1.0 for the aligned item", docs[0].Score)
	}
	if docs[0].Metadata["dir"] != "e" {
		t.Errorf("Metadata not carried over: %v", docs[0].Metadata)
	}
}

func TestVectorRetrieverNonPositiveKSkipsWork(t *testing.T) {
	r := retriever.NewVectorRetriever(vectorstore.NewInMemoryStore(), stubEmbedder{})
	docs, err := r.Retrieve(context.Background(), "query", 0)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("got %d docs, want 0 for k <= 0", len(docs))
	}
}
