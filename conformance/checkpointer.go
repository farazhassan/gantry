package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
)

// CheckpointerSuite verifies the contract of checkpointer.Checkpointer.
func CheckpointerSuite(t *testing.T, factory func() checkpointer.Checkpointer) {
	t.Helper()

	t.Run("save_then_load_round_trip", func(t *testing.T) {
		c := factory()
		ctx := context.Background()
		want := &gantry.State{Input: "in", Iteration: 7, FinalOutput: "out"}
		if err := c.Save(ctx, "id-1", want); err != nil {
			t.Fatalf("Save: %v", err)
		}
		got, err := c.Load(ctx, "id-1")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.Input != "in" || got.Iteration != 7 || got.FinalOutput != "out" {
			t.Errorf("got = %+v", got)
		}
	})

	t.Run("load_unknown_returns_error", func(t *testing.T) {
		c := factory()
		_, err := c.Load(context.Background(), "ghost")
		if err == nil {
			t.Fatalf("expected error for unknown id")
		}
		if !errors.Is(err, checkpointer.ErrNotFound) {
			t.Errorf("expected error to wrap checkpointer.ErrNotFound, got %v", err)
		}
	})

	t.Run("overwrite_same_id", func(t *testing.T) {
		c := factory()
		ctx := context.Background()
		_ = c.Save(ctx, "id-2", &gantry.State{Input: "v1"})
		_ = c.Save(ctx, "id-2", &gantry.State{Input: "v2"})
		got, _ := c.Load(ctx, "id-2")
		if got.Input != "v2" {
			t.Errorf("Input = %q, want v2", got.Input)
		}
	})
}
