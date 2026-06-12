package planner_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestLLMPlannerProducesNonEmptyPlan(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "1. step one\n2. step two\n3. step three"})
	p := planner.NewLLM(mock, "Break down this task into numbered steps.")

	plan, err := p.Plan(context.Background(), "do the thing")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan == nil {
		t.Fatalf("plan is nil")
	}
	if plan.Goal != "do the thing" {
		t.Errorf("plan.Goal = %q, want %q", plan.Goal, "do the thing")
	}
	if len(plan.Steps) != 3 {
		t.Errorf("got %d steps, want 3; plan = %+v", len(plan.Steps), plan)
	}
}

func TestPhasePlanConstant(t *testing.T) {
	if planner.PhasePlan == "" {
		t.Errorf("PhasePlan constant is empty")
	}
}
