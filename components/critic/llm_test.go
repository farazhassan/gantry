package critic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/eval"
)

func TestLLMCriticAcceptVerdict(t *testing.T) {
	// LLMCritic uses a structured rubric; mock returns "PASS".
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "PASS: looks good"})
	c := critic.NewLLM(mock, "Reply PASS or FAIL.")

	state := &gantry.State{
		LastResponse: &gantry.LLMResponse{Content: "the sky is blue"},
	}
	v, err := c.Critique(context.Background(), state)
	if err != nil {
		t.Fatalf("Critique: %v", err)
	}
	if !v.Accept {
		t.Errorf("expected Accept=true; got %+v", v)
	}
}

func TestLLMCriticRejectVerdict(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "FAIL: too short"})
	c := critic.NewLLM(mock, "Reply PASS or FAIL.")

	state := &gantry.State{LastResponse: &gantry.LLMResponse{Content: "ok"}}
	v, err := c.Critique(context.Background(), state)
	if err != nil {
		t.Fatalf("Critique: %v", err)
	}
	if v.Accept {
		t.Errorf("expected Accept=false; got %+v", v)
	}
	if v.Reason == "" {
		t.Errorf("expected non-empty Reason on rejection")
	}
}

func TestCriticInterface(t *testing.T) {
	var _ critic.Critic = critic.NewLLM(eval.NewMockLLMClient(), "")
}

func TestLLMCriticRendersAcceptanceCriteria(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "PASS"})
	c := critic.NewLLM(mock, "Reply PASS or FAIL.")

	state := &gantry.State{
		LastResponse: &gantry.LLMResponse{Content: "here is the work"},
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{
			{Description: "design", AcceptanceCriteria: "API documented"},
			{Description: "build", AcceptanceCriteria: "tests pass"},
		}},
	}
	if _, err := c.Critique(context.Background(), state); err != nil {
		t.Fatalf("Critique: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	user := reqs[0].Messages[len(reqs[0].Messages)-1].Content
	for _, want := range []string{"API documented", "tests pass", "here is the work"} {
		if !strings.Contains(user, want) {
			t.Errorf("critic prompt missing %q; got:\n%s", want, user)
		}
	}
}

func TestLLMCriticNoPlanLeavesPromptUnchanged(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "PASS"})
	c := critic.NewLLM(mock, "Reply PASS or FAIL.")

	state := &gantry.State{LastResponse: &gantry.LLMResponse{Content: "the work"}}
	if _, err := c.Critique(context.Background(), state); err != nil {
		t.Fatalf("Critique: %v", err)
	}
	user := mock.Requests()[0].Messages[0].Content
	if user != "the work" {
		t.Errorf("user message = %q, want exactly %q (unchanged)", user, "the work")
	}
}

func TestLLMCriticPlanWithoutCriteriaLeavesPromptUnchanged(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "PASS"})
	c := critic.NewLLM(mock, "Reply PASS or FAIL.")

	// A plan with steps but no AcceptanceCriteria must not alter the prompt.
	state := &gantry.State{
		LastResponse: &gantry.LLMResponse{Content: "the work"},
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{
			{Description: "design"},
			{Description: "build"},
		}},
	}
	if _, err := c.Critique(context.Background(), state); err != nil {
		t.Fatalf("Critique: %v", err)
	}
	user := mock.Requests()[0].Messages[0].Content
	if user != "the work" {
		t.Errorf("user message = %q, want exactly %q (unchanged)", user, "the work")
	}
}
