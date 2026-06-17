package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const defaultBaseURL = "http://localhost:6333"

// Point is a vector plus its payload, ready to upsert. The payload should carry
// the document text (see Retriever's textKey).
type Point struct {
	ID      uint64
	Vector  []float32
	Payload map[string]any
}

// Hit is a single search result.
type Hit struct {
	ID      uint64
	Score   float64
	Payload map[string]any
}

// Store is a REST client bound to a single Qdrant collection. Safe for
// concurrent use.
type Store struct {
	baseURL    string
	collection string
	dim        int
	distance   string
	apiKey     string
	httpc      *http.Client
}

// Option configures a Store at construction.
type Option func(*Store)

// New returns a Store. WithCollection is required; it panics on an empty
// collection name (a programmer error).
func New(opts ...Option) *Store {
	s := &Store{
		baseURL:  defaultBaseURL,
		distance: "Cosine",
		httpc:    &http.Client{},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.collection == "" {
		panic("qdrant: New requires WithCollection")
	}
	return s
}

// WithBaseURL points the store at a non-default Qdrant endpoint. A trailing
// slash is trimmed.
func WithBaseURL(url string) Option {
	return func(s *Store) { s.baseURL = strings.TrimRight(url, "/") }
}

// WithCollection sets the collection name (required).
func WithCollection(name string) Option {
	return func(s *Store) { s.collection = name }
}

// WithDim sets the vector dimensionality used by EnsureCollection.
func WithDim(dim int) Option {
	return func(s *Store) { s.dim = dim }
}

// WithDistance sets the distance metric (default "Cosine"). Empty is ignored.
func WithDistance(d string) Option {
	return func(s *Store) {
		if d != "" {
			s.distance = d
		}
	}
}

// WithAPIKey sets the Qdrant api-key header. Empty is ignored.
func WithAPIKey(key string) Option {
	return func(s *Store) {
		if key != "" {
			s.apiKey = key
		}
	}
}

// WithHTTPClient supplies the *http.Client. A nil client is ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(s *Store) {
		if h != nil {
			s.httpc = h
		}
	}
}

// EnsureCollection creates the collection if it does not already exist. It is
// idempotent: an existing collection is left untouched.
func (s *Store) EnsureCollection(ctx context.Context) error {
	if s.dim <= 0 {
		return fmt.Errorf("qdrant: EnsureCollection requires a positive vector dimension (set WithDim); got %d", s.dim)
	}
	exists, err := s.collectionExists(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	body := createCollectionRequest{Vectors: vectorParams{Size: s.dim, Distance: s.distance}}
	return s.doJSON(ctx, http.MethodPut, "/collections/"+s.collection, body, nil)
}

func (s *Store) collectionExists(ctx context.Context) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/collections/"+s.collection, nil)
	if err != nil {
		return false, fmt.Errorf("qdrant: build request: %w", err)
	}
	s.auth(req)
	resp, err := s.httpc.Do(req)
	if err != nil {
		return false, fmt.Errorf("qdrant: do request: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode/100 == 2 {
		return true, nil
	}
	return false, fmt.Errorf("qdrant: get collection: status %d", resp.StatusCode)
}

// Upsert inserts or updates points in the collection.
func (s *Store) Upsert(ctx context.Context, points ...Point) error {
	if len(points) == 0 {
		return nil
	}
	wp := make([]wirePoint, len(points))
	for i, p := range points {
		wp[i] = wirePoint{ID: p.ID, Vector: p.Vector, Payload: p.Payload}
	}
	return s.doJSON(ctx, http.MethodPut, "/collections/"+s.collection+"/points", upsertRequest{Points: wp}, nil)
}

// Search returns the k nearest points to vector, with payloads.
func (s *Store) Search(ctx context.Context, vector []float32, k int) ([]Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	var out searchResponse
	body := searchRequest{Vector: vector, Limit: k, WithPayload: true}
	if err := s.doJSON(ctx, http.MethodPost, "/collections/"+s.collection+"/points/search", body, &out); err != nil {
		return nil, err
	}
	hits := make([]Hit, len(out.Result))
	for i, h := range out.Result {
		hits[i] = Hit{ID: h.ID, Score: h.Score, Payload: h.Payload}
	}
	return hits, nil
}

func (s *Store) auth(req *http.Request) {
	if s.apiKey != "" {
		req.Header.Set("api-key", s.apiKey)
	}
}

// doJSON marshals reqBody, performs the call, checks the status, and decodes
// the response into out when out is non-nil.
func (s *Store) doJSON(ctx context.Context, method, path string, reqBody, out any) error {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("qdrant: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("qdrant: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	s.auth(req)

	resp, err := s.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("qdrant: %s %s: status %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(b))
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("qdrant: decode response: %w", err)
		}
	}
	return nil
}
