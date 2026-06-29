package checkpointer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
)

// failingCheckpointer always errors on Save, to exercise the non-fatal
// failure path of New.
type failingCheckpointer struct{}

func (failingCheckpointer) Save(context.Context, string, *gantry.State) error {
	return errors.New("disk full")
}

func (failingCheckpointer) Load(context.Context, string) (*gantry.State, error) {
	return nil, errors.New("not implemented")
}

func TestWithCheckpointerSavesOnPhaseEnd(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	store := checkpointer.NewInMemory()
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(checkpointer.New(store, "run-1")); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	saved, err := store.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if saved.FinalOutput != "done" {
		t.Errorf("saved.FinalOutput = %q, want done", saved.FinalOutput)
	}
}

func TestWithCheckpointerSaveErrorIsNonFatalAndTraced(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(checkpointer.New(failingCheckpointer{}, "run-err")); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}

	// A Save failure must not abort the run.
	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Save failure must be non-fatal; Run returned %v", err)
	}

	// The failure must be recorded on the trace as a checkpoint_failed event
	// carrying a wrapped ErrCheckpointFailed and the checkpoint id.
	var found *gantry.TraceEvent
	for _, ev := range state.Trace.Snapshot() {
		if ev.Name == "checkpoint_failed" {
			e := ev
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatalf("expected a checkpoint_failed trace event; got none")
	}
	if !errors.Is(found.Err, gantry.ErrCheckpointFailed) {
		t.Errorf("trace event Err = %v, want wrapped ErrCheckpointFailed", found.Err)
	}
	if found.Attrs["id"] != "run-err" {
		t.Errorf("trace event id attr = %v, want run-err", found.Attrs["id"])
	}
}
