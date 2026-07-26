package conformance

import (
	"context"
	"math"
	"testing"

	"github.com/farazhassan/gantry/components/semantic"
)

// SemanticStoreSuite verifies the contract of semantic.Store. The factory
// receives the vector dimension the suite will use; implementations that fix
// their dimension at construction (e.g. sqlitevec) need it, others may ignore
// it.
func SemanticStoreSuite(t *testing.T, factory func(dim int) semantic.Store) {
	t.Helper()
	ctx := context.Background()

	seed := func(t *testing.T, s semantic.Store) {
		t.Helper()
		err := s.Add(ctx,
			semantic.Item{Text: "east", Vector: []float32{1, 0}},
			semantic.Item{Text: "north", Vector: []float32{0, 1}},
			semantic.Item{Text: "northeast", Vector: []float32{0.7071, 0.7071}},
		)
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	t.Run("search_orders_by_descending_similarity", func(t *testing.T) {
		s := factory(2)
		seed(t, s)
		hits, err := s.Search(ctx, []float32{1, 0}, 3)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 3 {
			t.Fatalf("got %d hits, want 3", len(hits))
		}
		want := []string{"east", "northeast", "north"}
		for i := range want {
			if hits[i].Text != want[i] {
				t.Errorf("hits[%d].Text = %q, want %q", i, hits[i].Text, want[i])
			}
		}
		for i := 1; i < len(hits); i++ {
			if hits[i].Score > hits[i-1].Score {
				t.Errorf("Score not descending at %d: %v > %v", i, hits[i].Score, hits[i-1].Score)
			}
		}
		if math.Abs(hits[0].Score-1.0) > 1e-4 {
			t.Errorf("perfect-match Score = %v, want ~1.0", hits[0].Score)
		}
		if math.Abs(hits[2].Score-0.0) > 1e-4 {
			t.Errorf("orthogonal Score = %v, want ~0.0", hits[2].Score)
		}
	})

	t.Run("search_limits_to_k", func(t *testing.T) {
		s := factory(2)
		seed(t, s)
		hits, err := s.Search(ctx, []float32{1, 0}, 2)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 2 {
			t.Errorf("got %d hits, want 2", len(hits))
		}
		if len(hits) == 2 && hits[0].Text != "east" {
			t.Errorf("hits[0].Text = %q, want %q (top-k must keep best matches)", hits[0].Text, "east")
		}
	})

	t.Run("search_k_exceeding_count_returns_all", func(t *testing.T) {
		s := factory(2)
		seed(t, s)
		if err := s.Add(ctx, semantic.Item{Text: "southeast", Vector: []float32{0.7071, -0.7071}}); err != nil {
			t.Fatalf("second Add: %v", err)
		}
		hits, err := s.Search(ctx, []float32{1, 0}, 10)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 4 {
			t.Fatalf("got %d hits, want all 4 (second Add must append, not replace)", len(hits))
		}
		if hits[0].Text != "east" {
			t.Errorf("hits[0].Text = %q, want %q", hits[0].Text, "east")
		}
	})

	t.Run("search_empty_store_returns_empty", func(t *testing.T) {
		s := factory(2)
		hits, err := s.Search(ctx, []float32{1, 0}, 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 0 {
			t.Errorf("got %d hits from empty store, want 0", len(hits))
		}
	})

	t.Run("search_nonpositive_k_returns_empty_no_error", func(t *testing.T) {
		s := factory(2)
		seed(t, s)
		for _, k := range []int{0, -1} {
			hits, err := s.Search(ctx, []float32{1, 0}, k)
			if err != nil {
				t.Fatalf("Search(k=%d): %v", k, err)
			}
			if len(hits) != 0 {
				t.Errorf("Search(k=%d) returned %d hits, want 0", k, len(hits))
			}
		}
	})

	t.Run("add_zero_items_is_noop", func(t *testing.T) {
		s := factory(2)
		if err := s.Add(ctx); err != nil {
			t.Fatalf("Add(): %v", err)
		}
		hits, err := s.Search(ctx, []float32{1, 0}, 5)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 0 {
			t.Errorf("store not empty after Add() with no items: %+v", hits)
		}
	})

	t.Run("metadata_round_trip", func(t *testing.T) {
		s := factory(2)
		err := s.Add(ctx, semantic.Item{
			Text:     "remembered",
			Vector:   []float32{1, 0},
			Metadata: map[string]any{"role": "user"},
		})
		if err != nil {
			t.Fatalf("Add: %v", err)
		}
		hits, err := s.Search(ctx, []float32{1, 0}, 1)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(hits) != 1 {
			t.Fatalf("got %d hits, want 1", len(hits))
		}
		if got := hits[0].Metadata["role"]; got != "user" {
			t.Errorf(`Metadata["role"] = %v, want "user"`, got)
		}
	})

	// Independence is at the struct level only: the semantic.Store contract
	// permits Metadata maps to alias the store, so this suite intentionally
	// does not assert deep independence.
	t.Run("hits_are_independent_at_struct_level", func(t *testing.T) {
		s := factory(2)
		seed(t, s)
		first, err := s.Search(ctx, []float32{1, 0}, 1)
		if err != nil || len(first) != 1 {
			t.Fatalf("Search: hits=%d err=%v", len(first), err)
		}
		first[0].Text = "mutated"
		second, err := s.Search(ctx, []float32{1, 0}, 1)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if second[0].Text != "east" {
			t.Errorf("store affected by mutating a returned hit; got %q", second[0].Text)
		}
	})
}
