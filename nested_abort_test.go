package gantry_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// errLLM fails every Generate call with err.
type errLLM struct{ err error }

func (l errLLM) Generate(context.Context, gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{}, l.err
}

func nestedCtx() context.Context {
	return gantry.WithParentLink(context.Background(), gantry.ParentLink{
		RunID:      "run-parent",
		ToolCallID: "call-1",
		Name:       "researcher",
	})
}

// runRecording runs a with RunStream and returns every event and the run error.
func runRecording(t *testing.T, ctx context.Context, a *gantry.Agent) ([]gantry.Event, error) {
	t.Helper()
	var events []gantry.Event
	_, err := a.RunStream(ctx, "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	return events, err
}

func doneEvents(events []gantry.Event) []gantry.Event {
	var out []gantry.Event
	for _, ev := range events {
		if ev.Type == gantry.EventDone {
			out = append(out, ev)
		}
	}
	return out
}

func TestNestedRunLLMErrorStillEmitsDone(t *testing.T) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(errLLM{err: errors.New("model reply leaked tool-call markup")}),
		gantry.WithName("critic"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	events, runErr := runRecording(t, nestedCtx(), a)
	if runErr == nil {
		t.Fatal("RunStream returned nil error, want the LLM error")
	}
	dones := doneEvents(events)
	if len(dones) != 1 {
		t.Fatalf("done events = %d, want exactly 1", len(dones))
	}
	done := dones[0]
	if done.DoneReason != gantry.DoneError {
		t.Errorf("DoneReason = %q, want %q", done.DoneReason, gantry.DoneError)
	}
	if !strings.Contains(done.Error, "leaked tool-call markup") {
		t.Errorf("Error = %q, want it to carry the LLM error", done.Error)
	}
	if done.ParentRunID != "run-parent" || done.ParentToolCallID != "call-1" || done.Agent != "critic" {
		t.Errorf("done identity = %+v, want the nested run's identity", done)
	}
	if done.RunID == "" || done.RunID != events[0].RunID {
		t.Errorf("done RunID = %q, want the run's own RunID %q", done.RunID, events[0].RunID)
	}
	if events[len(events)-1].Type != gantry.EventDone {
		t.Errorf("last event = %q, want done", events[len(events)-1].Type)
	}
}

func TestNestedRunBlockingSentinelSetsDoneReason(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(errLLM{err: fmt.Errorf("%w: output matched", gantry.ErrGuardrailBlocked)}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	events, _ := runRecording(t, nestedCtx(), a)
	dones := doneEvents(events)
	if len(dones) != 1 || dones[0].DoneReason != gantry.DoneGuardrailBlocked {
		t.Fatalf("done events = %+v, want one with DoneReason %q", dones, gantry.DoneGuardrailBlocked)
	}
}

func TestTopLevelRunErrorEmitsNoDone(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(errLLM{err: errors.New("boom")}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	events, runErr := runRecording(t, context.Background(), a)
	if runErr == nil {
		t.Fatal("RunStream returned nil error, want the LLM error")
	}
	if dones := doneEvents(events); len(dones) != 0 {
		t.Errorf("done events = %+v, want none for a top-level run (its error return is the signal)", dones)
	}
}

func TestNestedRunDoneSinkErrorIsNotRetried(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	dones := 0
	_, runErr := a.RunStream(nestedCtx(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventDone {
			dones++
			return errors.New("sink closed")
		}
		return nil
	})
	if runErr == nil {
		t.Fatal("RunStream returned nil error, want the sink error")
	}
	if dones != 1 {
		t.Errorf("done events delivered = %d, want 1 (a failed done emit must not be followed by another)", dones)
	}
}
