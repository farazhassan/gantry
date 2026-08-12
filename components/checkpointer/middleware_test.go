package checkpointer_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/components/humanloop"
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
	store := mem.New()
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

// spySave records every Save call's Iteration/Done/DoneReason without
// persisting anything, so tests can assert exactly when and how many times
// a checkpointer.New middleware saved.
type spySave struct {
	mu    sync.Mutex
	calls []spyCall
}

type spyCall struct {
	Iteration  int
	Done       bool
	DoneReason gantry.DoneReason
}

func (s *spySave) Save(_ context.Context, _ string, st *gantry.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, spyCall{Iteration: st.Iteration, Done: st.Done, DoneReason: st.DoneReason})
	return nil
}

func (s *spySave) Load(context.Context, string) (*gantry.State, error) {
	return nil, errors.New("not implemented")
}

func (s *spySave) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// rejectingHumanInLoop always denies, to exercise the abort path.
type rejectingHumanInLoop struct{}

func (rejectingHumanInLoop) Confirm(context.Context, humanloop.Action) (humanloop.Decision, error) {
	return humanloop.Decision{Approved: false, Reason: "no"}, nil
}

// toolCallThenEndResponses is a two-turn script: the first LLM response has
// a pending tool call (so the loop runs a second iteration instead of
// finishing after the first), the second has none (so the loop ends
// normally). Used to force at least one full iteration before Done.
func toolCallThenEndResponses() []gantry.LLMResponse {
	return []gantry.LLMResponse{
		{
			Content:    "calling tool",
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop", Input: json.RawMessage("{}")}},
			StopReason: gantry.StopReasonToolUse,
		},
		{Content: "done", StopReason: gantry.StopReasonEnd},
	}
}

func TestNewWithExtraPhaseSavesMidRun(t *testing.T) {
	mock := eval.NewMockLLMClient(toolCallThenEndResponses()...)
	spy := &spySave{}
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(checkpointer.New(spy, "run-mid", gantry.PhaseObserve)); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if spy.len() != 2 {
		t.Fatalf("Save called %d times, want 2 (one PhaseObserve mid-run save + one final PhaseEnd save); calls=%+v", spy.len(), spy.calls)
	}
	if spy.calls[0].Done {
		t.Errorf("first save (mid-run, iteration 0): Done = true, want false")
	}
	if !spy.calls[1].Done {
		t.Errorf("second save (PhaseEnd): Done = false, want true")
	}
}

func TestNewExtraPhaseSavesAbortedStateEvenWithoutPhaseEnd(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "calling tool",
		ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop", Input: json.RawMessage("{}")}},
		StopReason: gantry.StopReasonToolUse,
	})
	spy := &spySave{}
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(humanloop.New(rejectingHumanInLoop{})); err != nil {
		t.Fatalf("install humanloop: %v", err)
	}
	// checkpointer installed AFTER humanloop: per Compose's registration
	// order (middleware.go), the last-registered middleware on a phase is
	// outermost, so this wraps humanloop's and observes its error.
	if err := a.With(checkpointer.New(spy, "run-abort", gantry.PhaseToolExec)); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if !errors.Is(err, gantry.ErrHumanAborted) {
		t.Fatalf("Run error = %v, want wrapped ErrHumanAborted", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneHumanAborted {
		t.Fatalf("state.Done=%v state.DoneReason=%q, want Done=true DoneReason=DoneHumanAborted", state.Done, state.DoneReason)
	}

	if spy.len() != 1 {
		t.Fatalf("Save called %d times, want 1 — PhaseEnd is never reached on this abort path, only the extraPhases save fires", spy.len())
	}
	if !spy.calls[0].Done || spy.calls[0].DoneReason != gantry.DoneHumanAborted {
		t.Errorf("saved call = %+v, want the aborted Done/DoneReason", spy.calls[0])
	}
}

func TestNewExtraPhaseOrderingMattersOnAbort(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "calling tool",
		ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop", Input: json.RawMessage("{}")}},
		StopReason: gantry.StopReasonToolUse,
	})
	spy := &spySave{}
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	// Wrong order: checkpointer installed BEFORE humanloop, so it ends up
	// innermost — humanloop's rejection returns without ever calling into
	// it. This documents the registration-order requirement on New.
	if err := a.With(checkpointer.New(spy, "run-wrong-order", gantry.PhaseToolExec)); err != nil {
		t.Fatalf("install checkpointer: %v", err)
	}
	if err := a.With(humanloop.New(rejectingHumanInLoop{})); err != nil {
		t.Fatalf("install humanloop: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); !errors.Is(err, gantry.ErrHumanAborted) {
		t.Fatalf("Run error = %v, want wrapped ErrHumanAborted", err)
	}

	if spy.len() != 0 {
		t.Fatalf("Save called %d times, want 0 — wrong registration order means the extraPhases hook never observes the abort", spy.len())
	}
}
