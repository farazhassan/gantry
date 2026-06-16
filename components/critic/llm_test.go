package critic_test

import (
	"context"
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
