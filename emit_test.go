package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// emittingMiddleware installs a PhaseObserve middleware that calls
// gantry.Emit directly, the way components/planner's update_plan
// interception will (a later task) — this proves Emit is usable, and stamps
// identity correctly, from outside the gantry package.
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
