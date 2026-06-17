package qdrant_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/farazhassan/gantry/components/qdrant"
)

func TestNewPanicsOnEmptyCollection(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New without collection: want panic, got none")
		}
	}()
	qdrant.New(qdrant.WithDim(3))
}

func TestEnsureCollectionCreatesWhenMissing(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			http.NotFound(w, r) // collection does not exist yet
			return
		}
		method, path = r.Method, r.URL.Path
		var body struct {
			Vectors struct {
				Size     int    `json:"size"`
				Distance string `json:"distance"`
			} `json:"vectors"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Vectors.Size != 3 || body.Vectors.Distance != "Cosine" {
			t.Errorf("create body = %+v, want size 3 / Cosine", body.Vectors)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(3),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	if err := s.EnsureCollection(context.Background()); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if method != http.MethodPut || path != "/collections/docs" {
		t.Errorf("create call = %s %s, want PUT /collections/docs", method, path)
	}
}

func TestEnsureCollectionNoOpWhenExists(t *testing.T) {
	putCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK) // already exists
			return
		}
		putCalled = true
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(3),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	if err := s.EnsureCollection(context.Background()); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if putCalled {
		t.Error("EnsureCollection created a collection that already exists")
	}
}

func TestEnsureCollectionFailsWithoutDim(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	if err := s.EnsureCollection(context.Background()); err == nil {
		t.Error("EnsureCollection without WithDim: want error, got nil")
	}
	if called {
		t.Error("EnsureCollection made an HTTP call despite missing dimension")
	}
}

func TestSearchNonPositiveKReturnsNoHits(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(2),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	hits, err := s.Search(context.Background(), []float32{0.1, 0.2}, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits, want 0", len(hits))
	}
	if called {
		t.Error("Search made an HTTP call for k <= 0")
	}
}

func TestUpsertSendsPoints(t *testing.T) {
	var path string
	var pointCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		var body struct {
			Points []struct {
				ID      uint64         `json:"id"`
				Vector  []float32      `json:"vector"`
				Payload map[string]any `json:"payload"`
			} `json:"points"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		pointCount = len(body.Points)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(2),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	err := s.Upsert(context.Background(),
		qdrant.Point{ID: 1, Vector: []float32{0.1, 0.2}, Payload: map[string]any{"content": "a"}},
		qdrant.Point{ID: 2, Vector: []float32{0.3, 0.4}, Payload: map[string]any{"content": "b"}},
	)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if path != "/collections/docs/points" || pointCount != 2 {
		t.Errorf("Upsert call = %s with %d points, want /collections/docs/points with 2", path, pointCount)
	}
}

func TestSearchReturnsHits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/collections/docs/points/search" {
			t.Errorf("path = %q, want /collections/docs/points/search", got)
		}
		var body struct {
			Vector      []float32 `json:"vector"`
			Limit       int       `json:"limit"`
			WithPayload bool      `json:"with_payload"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Limit != 5 || !body.WithPayload {
			t.Errorf("search body = %+v, want limit 5 / with_payload true", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": []map[string]any{
				{"id": 7, "score": 0.9, "payload": map[string]any{"content": "hello"}},
			},
		})
	}))
	defer srv.Close()

	s := qdrant.New(qdrant.WithCollection("docs"), qdrant.WithDim(2),
		qdrant.WithBaseURL(srv.URL), qdrant.WithHTTPClient(srv.Client()))
	hits, err := s.Search(context.Background(), []float32{0.1, 0.2}, 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].ID != 7 || hits[0].Score != 0.9 || hits[0].Payload["content"] != "hello" {
		t.Errorf("Search = %+v, want one hit id 7 score 0.9 content hello", hits)
	}
}
