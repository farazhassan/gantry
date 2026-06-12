package checkpointer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/harness"
)

func TestInMemoryCheckpointerRoundTrip(t *testing.T) {
	c := checkpointer.NewInMemory()
	original := &harness.State{Input: "x", Iteration: 3, FinalOutput: "y"}

	if err := c.Save(context.Background(), "run-1", original); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := c.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Input != "x" || got.Iteration != 3 || got.FinalOutput != "y" {
		t.Errorf("Loaded state = %+v", got)
	}
}

func TestInMemoryCheckpointerLoadUnknown(t *testing.T) {
	c := checkpointer.NewInMemory()
	_, err := c.Load(context.Background(), "ghost")
	if err == nil {
		t.Errorf("expected error loading unknown checkpoint")
	}
}

func TestCheckpointerInterface(t *testing.T) {
	var _ checkpointer.Checkpointer = checkpointer.NewInMemory()
}

func TestInMemoryCheckpointerLoadUnknownIsErrNotFound(t *testing.T) {
	c := checkpointer.NewInMemory()
	_, err := c.Load(context.Background(), "ghost")
	if !errors.Is(err, checkpointer.ErrNotFound) {
		t.Errorf("Load unknown id: got %v, want errors.Is(..., ErrNotFound)", err)
	}
}
