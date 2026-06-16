package qdrant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/farazhassan/gantry/components/qdrant"
	"github.com/farazhassan/gantry/components/retriever"
)

// stubEmbedder returns a fixed vector for any input, recording the texts it saw.
type stubEmbedder struct{ seen []string }

func (e *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.seen = append(e.seen, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{0.5, 0.5}
	}
	return out, nil
}

// Compile-time guarantee the adapter satisfies retriever.Retriever.
var _ retriever.Retriever = qdrant.NewRetriever(
	qdrant.New(qdrant.WithCollection("x"), qdrant.WithDim(2)), &stubEmbedder{})

func TestRetrieveEmbedsQueryAndMapsHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": 1, "score": 0.91, "payload": map[string]any{"content": "alpha", "title": "A"}},
				{"id": 2, "score": 0.80, "payload": map[string]any{"content": "beta", "title": "B"}},
			},
		})
	}))
	defer srv.Close()

	emb := &stubEmbedder{}
	store := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(2),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	r := qdrant.NewRetriever(store, emb)

	docs, err := r.Retrieve(context.Background(), "find alpha", 2)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(emb.seen) != 1 || emb.seen[0] != "find alpha" {
		t.Errorf("embedder saw %v, want [find alpha]", emb.seen)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
	if docs[0].ID != "1" || docs[0].Content != "alpha" || docs[0].Score != 0.91 {
		t.Errorf("doc[0] = %+v, want id 1 / content alpha / score 0.91", docs[0])
	}
	// Content key is lifted out; remaining payload stays in Metadata.
	if docs[0].Metadata["title"] != "A" {
		t.Errorf("doc[0].Metadata = %v, want title A", docs[0].Metadata)
	}
	if _, ok := docs[0].Metadata["content"]; ok {
		t.Error("doc[0].Metadata still contains the content key")
	}
}
