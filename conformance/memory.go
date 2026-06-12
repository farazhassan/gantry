package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/harness"
)

// MemorySuite verifies the contract of memory.Memory.
func MemorySuite(t *testing.T, factory func() memory.Memory) {
	t.Helper()

	t.Run("append_then_read_returns_in_order", func(t *testing.T) {
		m := factory()
		ctx := context.Background()
		msgs := []harness.Message{
			{Role: harness.RoleUser, Content: "a"},
			{Role: harness.RoleAssistant, Content: "b"},
			{Role: harness.RoleUser, Content: "c"},
		}
		for _, msg := range msgs {
			if err := m.Append(ctx, msg); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		got, err := m.Read(ctx)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(got) != len(msgs) {
			t.Fatalf("got %d messages, want %d", len(got), len(msgs))
		}
		for i := range msgs {
			if got[i].Content != msgs[i].Content {
				t.Errorf("[%d] = %q, want %q", i, got[i].Content, msgs[i].Content)
			}
		}
	})

	// Independence is at the slice level only (see Memory.Read): mutating a
	// returned element must not affect the store. The contract intentionally
	// does NOT promise deep independence of ToolCalls/Input, so this suite does
	// not assert it.
	t.Run("read_returns_independent_copy", func(t *testing.T) {
		m := factory()
		ctx := context.Background()
		_ = m.Append(ctx, harness.Message{Content: "x"})
		first, _ := m.Read(ctx)
		if len(first) == 0 {
			t.Fatalf("expected at least one message")
		}
		first[0].Content = "mutated"
		second, _ := m.Read(ctx)
		if second[0].Content != "x" {
			t.Errorf("Read returned aliased slice; got %q", second[0].Content)
		}
	})

	t.Run("read_on_empty_returns_empty_slice", func(t *testing.T) {
		m := factory()
		got, err := m.Read(context.Background())
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty; got %+v", got)
		}
	})
}
