package mem_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
)

func TestNewRoundTrip(t *testing.T) {
	c := mem.New()
	original := &gantry.State{Input: "x", Iteration: 3, FinalOutput: "y"}

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

func TestNewLoadUnknown(t *testing.T) {
	c := mem.New()
	_, err := c.Load(context.Background(), "ghost")
	if err == nil {
		t.Errorf("expected error loading unknown checkpoint")
	}
}

func TestNewSaveNilStateErrors(t *testing.T) {
	c := mem.New()
	if err := c.Save(context.Background(), "run-1", nil); err == nil {
		t.Fatal("want error saving nil state, got nil")
	}
}

func TestNewImplementsCheckpointer(t *testing.T) {
	var _ checkpointer.Checkpointer = mem.New()
}

func TestNewLoadUnknownIsErrNotFound(t *testing.T) {
	c := mem.New()
	_, err := c.Load(context.Background(), "ghost")
	if !errors.Is(err, checkpointer.ErrNotFound) {
		t.Errorf("Load unknown id: got %v, want errors.Is(..., ErrNotFound)", err)
	}
}
