package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/harness"
)

// CompactorSuite verifies the contract of compactor.Compactor.
func CompactorSuite(t *testing.T, factory func() compactor.Compactor) {
	t.Helper()

	t.Run("compact_preserves_or_shrinks_length", func(t *testing.T) {
		c := factory()
		msgs := []harness.Message{
			{Content: "a"}, {Content: "b"}, {Content: "c"}, {Content: "d"}, {Content: "e"},
		}
		got, err := c.Compact(context.Background(), msgs, compactor.Budget{MaxTokens: 10})
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		if len(got) > len(msgs) {
			t.Errorf("Compact added messages: %d > %d", len(got), len(msgs))
		}
	})

	t.Run("compact_empty_returns_empty", func(t *testing.T) {
		c := factory()
		got, err := c.Compact(context.Background(), nil, compactor.Budget{})
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty; got %+v", got)
		}
	})

	// Independence is at the slice level only (see Compactor.Compact): mutating
	// a returned element must not affect the input. The contract intentionally
	// does NOT promise deep independence of ToolCalls/Input, so this suite does
	// not assert it.
	t.Run("compact_returns_independent_copy", func(t *testing.T) {
		c := factory()
		msgs := []harness.Message{
			{Content: "a"}, {Content: "b"}, {Content: "c"}, {Content: "d"}, {Content: "e"},
		}
		before := make([]string, len(msgs))
		for i, m := range msgs {
			before[i] = m.Content
		}
		got, err := c.Compact(context.Background(), msgs, compactor.Budget{MaxTokens: 10})
		if err != nil {
			t.Fatalf("Compact: %v", err)
		}
		// Mutate every returned element; the input slice must be unaffected
		// regardless of which window the compactor returned.
		for i := range got {
			got[i].Content = "MUTATED"
		}
		for i, m := range msgs {
			if m.Content != before[i] {
				t.Errorf("Compact aliased input: msgs[%d] = %q, want %q", i, m.Content, before[i])
			}
		}
	})
}
