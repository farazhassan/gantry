package gantry_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// emittingMiddleware installs a PhaseObserve middleware that calls
// gantry.Emit directly, proving Emit is usable — and stamps identity
// correctly — from a middleware installed outside the gantry package.
// PhaseObserve is just a convenient phase for this test; Emit's identity
// stamping only reads ctx, not Phase, so which phase calls it doesn't
// matter. (components/planner's real update_plan interception, a later
// task, calls Emit from PhaseToolExec instead.)
func emittingMiddleware(step *gantry.PlanStep) gantry.Middleware {
	return func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := gantry.Emit(ctx, gantry.Event{
				Type:      gantry.EventPlanStepChanged,
				Iteration: s.Iteration,
				PlanStep:  step,
			}); err != nil {
				return err
			}
			return next(ctx, s)
		}
	}
}

func TestEmitStampsIdentityFromOutsidePackage(t *testing.T) {
	// PhaseObserve only runs when the current iteration has not already been
	// marked Done: the run loop breaks out of the phase list as soon as
	// DefaultPostLLMHandler sets state.Done (which happens whenever an LLM
	// turn returns no tool calls, see default_handlers.go). So the mock
	// script needs a first turn WITH a tool call (state.Done stays false,
	// PhaseToolExec and PhaseObserve both run) before the final turn that
	// ends the run.
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "call1", Name: "update_plan", Input: []byte(`{}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("planner-agent"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	step := &gantry.PlanStep{ID: "s1", Description: "design", Status: gantry.StepDone}
	a.Use(gantry.PhaseObserve, emittingMiddleware(step))

	var got *gantry.Event
	_, err = a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventPlanStepChanged {
			e := ev
			got = &e
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if got == nil {
		t.Fatal("no EventPlanStepChanged observed")
	}
	if got.Agent != "planner-agent" {
		t.Errorf("Agent = %q, want planner-agent (Emit must stamp ambient identity)", got.Agent)
	}
	if got.RunID == "" {
		t.Error("RunID is empty, want the run's minted id")
	}
	if got.PlanStep == nil || got.PlanStep.ID != "s1" || got.PlanStep.Status != gantry.StepDone {
		t.Errorf("PlanStep = %+v, want the step passed to Emit", got.PlanStep)
	}
}

func TestSetStateWithoutSinkIsNoop(t *testing.T) {
	if err := gantry.SetState(context.Background(), map[string]any{"a": 1}); err != nil {
		t.Fatalf("SetState with no sink = %v, want nil", err)
	}
}

func TestSetStateEmitsStateChanged(t *testing.T) {
	var got []gantry.Event
	ctx := gantry.WithSink(context.Background(), func(ev gantry.Event) error {
		got = append(got, ev)
		return nil
	})
	snap := map[string]any{"suggestions": []any{map[string]any{"title": "t", "prompt": "p"}}}
	if err := gantry.SetState(ctx, snap); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1: %+v", len(got), got)
	}
	if got[0].Type != gantry.EventStateChanged {
		t.Errorf("Type = %q, want %q", got[0].Type, gantry.EventStateChanged)
	}
	if !reflect.DeepEqual(got[0].State, snap) {
		t.Errorf("State = %#v, want %#v", got[0].State, snap)
	}
	if got[0].Timestamp.IsZero() {
		t.Error("Timestamp is zero, want it stamped by emit()")
	}
}

func TestSetStateReturnsSinkError(t *testing.T) {
	boom := errors.New("boom")
	ctx := gantry.WithSink(context.Background(), func(gantry.Event) error { return boom })
	if err := gantry.SetState(ctx, map[string]any{}); !errors.Is(err, boom) {
		t.Fatalf("SetState = %v, want the sink's error %v", err, boom)
	}
}

// TestSetStateStampsRunIdentity proves SetState, called from a middleware
// installed outside the gantry package, gets the run's identity like any
// other event: it goes through Emit, not a separate path. PhaseStart runs
// once per run after the identity is installed on ctx (agent.go: withIdentity
// before runPhase(PhaseStart)), so a single final LLM turn is enough here.
func TestSetStateStampsRunIdentity(t *testing.T) {
	a, err := gantry.NewAgent(
		gantry.WithLLM(eval.NewMockLLMClient(
			gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
		)),
		gantry.WithName("orchestrator"),
	)
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	snap := map[string]any{"suggestions": []any{}}
	a.Use(gantry.PhaseStart, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := gantry.SetState(ctx, snap); err != nil {
				return err
			}
			return next(ctx, s)
		}
	})

	var got *gantry.Event
	_, err = a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventStateChanged {
			e := ev
			got = &e
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if got == nil {
		t.Fatal("no EventStateChanged observed")
	}
	if got.Agent != "orchestrator" {
		t.Errorf("Agent = %q, want orchestrator (SetState must stamp ambient identity)", got.Agent)
	}
	if got.RunID == "" {
		t.Error("RunID is empty, want the run's minted id")
	}
	if got.ParentRunID != "" || got.ParentToolCallID != "" {
		t.Errorf("parent link = %q/%q, want empty for a top-level run", got.ParentRunID, got.ParentToolCallID)
	}
	if !reflect.DeepEqual(got.State, snap) {
		t.Errorf("State = %#v, want %#v", got.State, snap)
	}
}

func TestEventStateJSON(t *testing.T) {
	b, err := json.Marshal(gantry.Event{Type: gantry.EventStateChanged, State: map[string]any{"k": "v"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"type":"state_changed"`, `"state":{"k":"v"}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("JSON %s missing %s", b, want)
		}
	}
	b2, err := json.Marshal(gantry.Event{Type: gantry.EventDone})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b2), `"state"`) {
		t.Errorf("JSON %s has a state key, want it omitted when State is nil", b2)
	}
}
