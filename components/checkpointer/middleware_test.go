package checkpointer_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// failingCheckpointer always errors on Save, to exercise the non-fatal
// failure path of WithCheckpointer.
type failingCheckpointer struct{}

func (failingCheckpointer) Save(context.Context, string, *harness.State) error {
	return errors.New("disk full")
}

func (failingCheckpointer) Load(context.Context, string) (*harness.State, error) {
	return nil, errors.New("not implemented")
}

func TestWithCheckpointerSavesOnPhaseEnd(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "done", StopReason: harness.StopReasonEnd})
	store := checkpointer.NewInMemory()
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	checkpointer.WithCheckpointer(a, store, "run-1")

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
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "done", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	checkpointer.WithCheckpointer(a, failingCheckpointer{}, "run-err")

	// A Save failure must not abort the run.
	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Save failure must be non-fatal; Run returned %v", err)
	}

	// The failure must be recorded on the trace as a checkpoint_failed event
	// carrying a wrapped ErrCheckpointFailed and the checkpoint id.
	var found *harness.TraceEvent
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
	if !errors.Is(found.Err, harness.ErrCheckpointFailed) {
		t.Errorf("trace event Err = %v, want wrapped ErrCheckpointFailed", found.Err)
	}
	if found.Attrs["id"] != "run-err" {
		t.Errorf("trace event id attr = %v, want run-err", found.Attrs["id"])
	}
}
