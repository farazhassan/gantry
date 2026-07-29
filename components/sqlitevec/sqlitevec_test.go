package sqlitevec_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/farazhassan/gantry/components/sqlitevec"
	"github.com/farazhassan/gantry/components/vectorstore"
	"github.com/farazhassan/gantry/conformance"
)

// Compile-time guarantee the backend satisfies vectorstore.Store.
var _ vectorstore.Store = (*sqlitevec.Store)(nil)

func TestConformance(t *testing.T) {
	conformance.VectorStoreSuite(t, func(dim int) vectorstore.Store {
		return newStore(t, dim)
	})
}

func newStore(t *testing.T, dim int) *sqlitevec.Store {
	t.Helper()
	s, err := sqlitevec.New(filepath.Join(t.TempDir(), "mem.db"), sqlitevec.WithDim(dim))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestNewCreatesSchemaAndClose(t *testing.T) {
	s := newStore(t, 2)
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewPanicsWithoutDim(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New without WithDim did not panic")
		}
	}()
	_, _ = sqlitevec.New(":memory:")
}

func TestNewRejectsInvalidTableName(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New with an invalid table name did not panic")
		}
	}()
	_, _ = sqlitevec.New(":memory:", sqlitevec.WithDim(2),
		sqlitevec.WithTable("bad name; drop--"))
}

func TestReopenSameFileKeepsData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mem.db")

	s1, err := sqlitevec.New(path, sqlitevec.WithDim(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s1.Add(ctx, vectorstore.Item{Text: "durable", Vector: []float32{1, 0}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopening must be idempotent on schema and see the stored data.
	s2, err := sqlitevec.New(path, sqlitevec.WithDim(2))
	if err != nil {
		t.Fatalf("reopen New: %v", err)
	}
	defer s2.Close()
	hits, err := s2.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Text != "durable" {
		t.Errorf("hits = %+v, want the persisted item", hits)
	}
}

func TestDimensionMismatchErrors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 2)
	if err := s.Add(ctx, vectorstore.Item{Text: "x", Vector: []float32{1, 0, 0}}); err == nil {
		t.Error("Add with wrong dimension did not error")
	}
	if _, err := s.Search(ctx, []float32{1}, 1); err == nil {
		t.Error("Search with wrong dimension did not error")
	}
}

func TestNilMetadataRoundTripsAsNil(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 2)
	if err := s.Add(ctx, vectorstore.Item{Text: "bare", Vector: []float32{1, 0}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	if hits[0].Metadata != nil {
		t.Errorf("Metadata = %v, want nil for item stored without metadata", hits[0].Metadata)
	}
}

func TestInMemoryPath(t *testing.T) {
	ctx := context.Background()
	s, err := sqlitevec.New(":memory:", sqlitevec.WithDim(2))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Add(ctx, vectorstore.Item{Text: "ephemeral", Vector: []float32{1, 0}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Text != "ephemeral" {
		t.Errorf("hits = %+v, want the in-memory item", hits)
	}
}

func TestWithTableUsesCustomTables(t *testing.T) {
	ctx := context.Background()
	s, err := sqlitevec.New(filepath.Join(t.TempDir(), "mem.db"),
		sqlitevec.WithDim(2), sqlitevec.WithTable("agent_a"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()
	if err := s.Add(ctx, vectorstore.Item{Text: "scoped", Vector: []float32{1, 0}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 || hits[0].Text != "scoped" {
		t.Errorf("hits = %+v, want the item in the custom table", hits)
	}
}

func TestNumericMetadataRoundTripsAsFloat64(t *testing.T) {
	ctx := context.Background()
	s := newStore(t, 2)
	if err := s.Add(ctx, vectorstore.Item{
		Text:     "counted",
		Vector:   []float32{1, 0},
		Metadata: map[string]any{"count": 5},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	hits, err := s.Search(ctx, []float32{1, 0}, 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1", len(hits))
	}
	// Metadata is JSON-serialized, so numbers decode as float64 (not int).
	// This is an intentional, documented divergence from InMemoryStore.
	if got, ok := hits[0].Metadata["count"].(float64); !ok || got != 5 {
		t.Errorf(`Metadata["count"] = %#v, want float64(5)`, hits[0].Metadata["count"])
	}
}
