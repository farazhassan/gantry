package planner_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
)

func TestLLMPlannerProducesNonEmptyPlan(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "1. step one\n2. step two\n3. step three"})
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

func TestLLMPlannerParsesAcceptanceCriteria(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content: "1. design the API :: endpoints documented and reviewed\n2. implement :: all tests pass",
	})
	p := planner.NewLLM(mock, "Break down the task.")

	plan, err := p.Plan(context.Background(), "build it")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("got %d steps, want 2; plan = %+v", len(plan.Steps), plan)
	}
	if plan.Steps[0].Description != "design the API" {
		t.Errorf("step0 Description = %q, want %q", plan.Steps[0].Description, "design the API")
	}
	if plan.Steps[0].AcceptanceCriteria != "endpoints documented and reviewed" {
		t.Errorf("step0 AcceptanceCriteria = %q, want %q", plan.Steps[0].AcceptanceCriteria, "endpoints documented and reviewed")
	}
	if plan.Steps[1].Description != "implement" || plan.Steps[1].AcceptanceCriteria != "all tests pass" {
		t.Errorf("step1 = %+v", plan.Steps[1])
	}
}

func TestLLMPlannerLineWithoutDelimiterHasEmptyCriteria(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "1. just a plain step"})
	p := planner.NewLLM(mock, "Break down the task.")

	plan, err := p.Plan(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(plan.Steps))
	}
	if plan.Steps[0].Description != "just a plain step" {
		t.Errorf("Description = %q, want %q", plan.Steps[0].Description, "just a plain step")
	}
	if plan.Steps[0].AcceptanceCriteria != "" {
		t.Errorf("AcceptanceCriteria = %q, want empty", plan.Steps[0].AcceptanceCriteria)
	}
}
