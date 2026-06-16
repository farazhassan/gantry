package planner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
)

func TestWithPlannerSetsStatePlanAndInjectsIntoSystem(t *testing.T) {
	plannerLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "1. first\n2. second"})
	mainLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})

	a, _ := gantry.NewAgent(gantry.WithLLM(mainLLM))
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
