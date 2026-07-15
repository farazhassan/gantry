package planner_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/task"
)

func replanFixtureTask() *task.Task {
	return &task.Task{
		ID:   "tk-1",
		Goal: "ship the feature",
		Plan: &gantry.Plan{Goal: "ship the feature", Steps: []gantry.PlanStep{
			{ID: "s1", Description: "design the API", Status: gantry.StepDone},
			{ID: "s2", Description: "implement", Status: gantry.StepFailed, Output: "build broke"},
		}},
	}
}

func TestLLMReplannerRendersLedgerAndReason(t *testing.T) {
	mock := eval.NewMockLLMClient(proposePlanResponse(
		`{"steps":[{"description":"fix the build","acceptance_criteria":["go build passes"]}]}`))
	r := planner.NewLLMReplanner(mock, "Revise the plan.")

	plan, err := r.Replan(context.Background(), replanFixtureTask(), "plan step failed: implement (build broke)")
	if err != nil {
		t.Fatalf("Replan: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Description != "fix the build" {
		t.Fatalf("plan = %+v, want one 'fix the build' step", plan)
	}
	if plan.Steps[0].AcceptanceCriteria != "go build passes" {
		t.Errorf("criteria = %q, want 'go build passes'", plan.Steps[0].AcceptanceCriteria)
	}
	if plan.Steps[0].ID != "" {
		t.Errorf("ID = %q, want empty (the ledger mints IDs at adoption)", plan.Steps[0].ID)
	}
	if plan.Goal != "ship the feature" {
		t.Errorf("Goal = %q, want the task goal", plan.Goal)
	}

	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("Generate called %d times, want 1", len(reqs))
	}
	req := reqs[0]
	if req.ToolChoice == nil || req.ToolChoice.Mode != gantry.ToolChoiceTool || req.ToolChoice.Name != "propose_plan" {
		t.Errorf("ToolChoice = %+v, want forced propose_plan", req.ToolChoice)
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "propose_plan" {
		t.Errorf("Tools = %+v, want exactly the propose_plan def", req.Tools)
	}
	if req.System != "Revise the plan." {
		t.Errorf("System = %q, want the rubric", req.System)
	}
	prompt := req.Messages[0].Content
	for _, want := range []string{
		"ship the feature", // goal
		"design the API",   // ledger step description
		"implement",        // ledger step description
		"s1",               // step id (plan 12's renderPlan renders ids)
		"failed",           // step status
		"plan step failed: implement (build broke)", // the reason
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestLLMReplannerErrorPropagates(t *testing.T) {
	sentinel := errors.New("llm down")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: sentinel}})
	r := planner.NewLLMReplanner(mock, "")
	if _, err := r.Replan(context.Background(), replanFixtureTask(), "reason"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the client error (no fallback here; the task.Driver degrades)", err)
	}
	if reqs := mock.Requests(); len(reqs) != 1 {
		t.Errorf("Generate called %d times, want 1 (no retry)", len(reqs))
	}
}

func TestLLMReplannerMissingToolCallIsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "just text"})
	r := planner.NewLLMReplanner(mock, "")
	if _, err := r.Replan(context.Background(), replanFixtureTask(), "reason"); err == nil {
		t.Fatal("Replan = nil error, want missing-tool-call error")
	}
}
