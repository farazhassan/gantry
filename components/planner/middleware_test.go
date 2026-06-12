package planner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithPlannerSetsStatePlanAndInjectsIntoSystem(t *testing.T) {
	plannerLLM := eval.NewMockLLMClient(harness.LLMResponse{Content: "1. first\n2. second"})
	mainLLM := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})

	a, _ := harness.New(harness.WithLLM(mainLLM))
	if err := planner.WithPlanner(a, planner.NewLLM(plannerLLM, "")); err != nil {
		t.Fatalf("WithPlanner: %v", err)
	}

	state, err := a.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Plan == nil || len(state.Plan.Steps) != 2 {
		t.Errorf("Plan = %+v", state.Plan)
	}
	req := mainLLM.Requests()[0]
	if !strings.Contains(req.System, "first") || !strings.Contains(req.System, "second") {
		t.Errorf("plan steps not in system prompt: %q", req.System)
	}
}
