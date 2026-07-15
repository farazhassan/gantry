package planner_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
)

// proposePlanResponse builds a scripted LLM response carrying a propose_plan
// tool call with the given JSON payload — the shape a provider returns when
// the forced ToolChoice is honored. Shared by the middleware and iteration
// tests in this package.
func proposePlanResponse(payload string) gantry.LLMResponse {
	return gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "propose_plan", Input: json.RawMessage(payload)}},
		StopReason: gantry.StopReasonToolUse,
	}
}

func TestLLMPlannerForcesProposePlanToolChoice(t *testing.T) {
	mock := eval.NewMockLLMClient(proposePlanResponse(`{"steps":[{"description":"step one"}]}`))
	p := planner.NewLLM(mock, "Break down this task.")

	if _, err := p.Plan(context.Background(), "do the thing"); err != nil {
		t.Fatalf("Plan: %v", err)
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
	if req.System != "Break down this task." {
		t.Errorf("System = %q, want the rubric", req.System)
	}
}

func TestLLMPlannerParsesStructuredSteps(t *testing.T) {
	mock := eval.NewMockLLMClient(proposePlanResponse(
		`{"steps":[` +
			`{"description":"design the API","acceptance_criteria":["endpoints documented","reviewed"]},` +
			`{"description":"implement","acceptance_criteria":["all tests pass"]},` +
			`{"description":"ship"}]}`))
	p := planner.NewLLM(mock, "Break down the task.")

	plan, err := p.Plan(context.Background(), "build it")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Goal != "build it" {
		t.Errorf("Goal = %q, want build it", plan.Goal)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("got %d steps, want 3; plan = %+v", len(plan.Steps), plan)
	}
	if plan.Steps[0].Description != "design the API" || plan.Steps[0].AcceptanceCriteria != "endpoints documented; reviewed" {
		t.Errorf("step0 = %+v, want criteria joined with '; '", plan.Steps[0])
	}
	if plan.Steps[1].AcceptanceCriteria != "all tests pass" {
		t.Errorf("step1 = %+v", plan.Steps[1])
	}
	if plan.Steps[2].AcceptanceCriteria != "" {
		t.Errorf("step2 criteria = %q, want empty", plan.Steps[2].AcceptanceCriteria)
	}
	for i, s := range plan.Steps {
		if s.ID != "" {
			t.Errorf("step %d ID = %q, want empty (IDs minted at ledger adoption, as today)", i, s.ID)
		}
	}
}

func TestLLMPlannerFallsBackToLegacyParserOnGenerateError(t *testing.T) {
	// Turn 1: the forced-ToolChoice request errors (e.g. the Ollama adapter
	// rejects forced modes client-side, per plan 01). Turn 2: the legacy
	// plain-text retry, parsed with newline splitting and " :: " criteria.
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: errors.New(`ollama: tool_choice mode "tool" is not supported by the Ollama API`)},
		{Response: gantry.LLMResponse{Content: "1. design the API :: endpoints reviewed\n2. implement"}},
	})
	p := planner.NewLLM(mock, "Break down the task.")

	plan, err := p.Plan(context.Background(), "build it")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("got %d steps, want 2; plan = %+v", len(plan.Steps), plan)
	}
	if plan.Steps[0].Description != "design the API" || plan.Steps[0].AcceptanceCriteria != "endpoints reviewed" {
		t.Errorf("step0 = %+v (legacy ' :: ' parsing broken)", plan.Steps[0])
	}
	if plan.Steps[1].Description != "implement" || plan.Steps[1].AcceptanceCriteria != "" {
		t.Errorf("step1 = %+v", plan.Steps[1])
	}
	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("Generate called %d times, want 2 (forced attempt + one legacy retry)", len(reqs))
	}
	if reqs[1].ToolChoice != nil || len(reqs[1].Tools) != 0 {
		t.Errorf("legacy retry must carry no tools: ToolChoice=%+v Tools=%+v", reqs[1].ToolChoice, reqs[1].Tools)
	}
}

func TestLLMPlannerFallbackErrorPropagates(t *testing.T) {
	// Both the forced attempt and the legacy retry fail: the retry's error is
	// returned and there is no further looping.
	sentinel := errors.New("llm down")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: errors.New("first failure")},
		{Err: sentinel},
	})
	p := planner.NewLLM(mock, "")
	if _, err := p.Plan(context.Background(), "x"); !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the legacy retry's error", err)
	}
	if reqs := mock.Requests(); len(reqs) != 2 {
		t.Errorf("Generate called %d times, want exactly 2", len(reqs))
	}
}

func TestLLMPlannerMissingToolCallIsError(t *testing.T) {
	// A SUCCESSFUL response without the forced call is a misbehaving client,
	// not a fallback trigger.
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "1. a text plan"})
	p := planner.NewLLM(mock, "")
	if _, err := p.Plan(context.Background(), "x"); err == nil {
		t.Fatal("Plan = nil error, want missing-tool-call error")
	}
	if reqs := mock.Requests(); len(reqs) != 1 {
		t.Errorf("Generate called %d times, want 1 (no fallback on a successful response)", len(reqs))
	}
}

func TestPhasePlanConstant(t *testing.T) {
	if planner.PhasePlan == "" {
		t.Errorf("PhasePlan constant is empty")
	}
}
