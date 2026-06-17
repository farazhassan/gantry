package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/farazhassan/gantry/components/embeddings"
	"github.com/farazhassan/gantry/components/embeddings/openai"
)

// Compile-time guarantee the client satisfies the interface.
var _ embeddings.Embeddings = (*openai.Client)(nil)

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(\"\"): want panic on empty model, got none")
		}
	}()
	openai.New("", openai.WithAPIKey("k"))
}

func TestNewPanicsOnMissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	defer func() {
		if recover() == nil {
			t.Error("New without key: want panic, got none")
		}
	}()
	openai.New("text-embedding-3-small")
}

func TestEmbedEmptyInputSkipsCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	c := openai.New("m", openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL),
		openai.WithHTTPClient(srv.Client()))
	got, err := c.Embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d vectors, want 0", len(got))
	}
	if called {
		t.Error("Embed made an HTTP call for empty input")
	}
}

func TestEmbedReturnsVectorsInInputOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v1/embeddings" {
			t.Errorf("path = %q, want /v1/embeddings", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth = %q, want Bearer k", got)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Input) != 2 {
			t.Errorf("input len = %d, want 2", len(req.Input))
		}
		// Return out of order to prove the client reorders by index.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 1, "embedding": []float32{0.3, 0.4}},
				{"index": 0, "embedding": []float32{0.1, 0.2}},
			},
		})
	}))
	defer srv.Close()

	c := openai.New("m", openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL),
		openai.WithHTTPClient(srv.Client()))
	got, err := c.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	if len(got) != 2 || got[0][0] != want[0][0] || got[1][0] != want[1][0] {
		t.Errorf("Embed = %v, want %v", got, want)
	}
}

func TestEmbedErrorsOnDuplicateIndex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Same length as input (2) but index 0 is duplicated and 1 is missing.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"index": 0, "embedding": []float32{0.1, 0.2}},
				{"index": 0, "embedding": []float32{0.3, 0.4}},
			},
		})
	}))
	defer srv.Close()

	c := openai.New("m", openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL),
		openai.WithHTTPClient(srv.Client()))
	if _, err := c.Embed(context.Background(), []string{"a", "b"}); err == nil {
		t.Error("Embed: want error on duplicate/missing index, got nil")
	}
}

func TestEmbedErrorsOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	}))
	defer srv.Close()

	c := openai.New("m", openai.WithAPIKey("k"), openai.WithBaseURL(srv.URL),
		openai.WithHTTPClient(srv.Client()))
	if _, err := c.Embed(context.Background(), []string{"a"}); err == nil {
		t.Error("Embed: want error on 400, got nil")
	}
}
