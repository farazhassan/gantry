package memory_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/conformance"
)

func TestInMemoryStoreSearchRanksBySimilarity(t *testing.T) {
	s := memory.NewInMemoryStore()
	ctx := context.Background()
	err := s.Add(ctx,
		memory.Item{Text: "east", Vector: []float32{1, 0}},
		memory.Item{Text: "north", Vector: []float32{0, 1}},
		memory.Item{Text: "northeast", Vector: []float32{0.7071, 0.7071}},
	)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Text != "east" || hits[1].Text != "northeast" {
		t.Errorf("hits = [%q, %q], want [east, northeast]", hits[0].Text, hits[1].Text)
	}
	if hits[0].Score < hits[1].Score {
		t.Errorf("scores not descending: %v then %v", hits[0].Score, hits[1].Score)
	}
}

func TestInMemoryStoreSearchNonPositiveK(t *testing.T) {
	s := memory.NewInMemoryStore()
	_ = s.Add(context.Background(), memory.Item{Text: "x", Vector: []float32{1, 0}})
	hits, err := s.Search(context.Background(), []float32{1, 0}, 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("got %d hits for k = 0, want 0", len(hits))
	}
}

func TestInMemoryStoreMismatchedDimensionScoresZero(t *testing.T) {
	s := memory.NewInMemoryStore()
	_ = s.Add(context.Background(), memory.Item{Text: "short", Vector: []float32{1}})
	hits, err := s.Search(context.Background(), []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Score != 0 {
		t.Errorf("hits = %+v, want one hit with Score 0", hits)
	}
}

func TestInMemoryStoreZeroMagnitudeVectorScoresZero(t *testing.T) {
	s := memory.NewInMemoryStore()
	_ = s.Add(context.Background(), memory.Item{Text: "real", Vector: []float32{1, 0}})
	hits, err := s.Search(context.Background(), []float32{0, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Score != 0 {
		t.Errorf("hits = %+v, want one hit with Score 0", hits)
	}
}

func TestInMemoryStoreConformance(t *testing.T) {
	conformance.MemoryStoreSuite(t, func(int) memory.Store {
		return memory.NewInMemoryStore()
	})
}
