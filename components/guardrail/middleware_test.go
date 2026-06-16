package guardrail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithGuardrailBlocksOnInputMatch(t *testing.T) {
	mock := eval.NewMockLLMClient() // should not be called
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)password`, guardrail.DirectionInput))

	state, err := a.Run(context.Background(), "what is my password")
	if err == nil || !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.DoneReason != harness.DoneGuardrailBlocked {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("LLM should not be called; got %d requests", len(mock.Requests()))
	}
}

func TestWithGuardrailBlocksOnOutputMatch(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "the secret is 42", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)secret`, guardrail.DirectionOutput))

	state, err := a.Run(context.Background(), "tell me")
	if err == nil || !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.DoneReason != harness.DoneGuardrailBlocked {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
}

func TestWithGuardrailOutputBlockScrubsFinalOutput(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "the secret is 42", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)secret`, guardrail.DirectionOutput))

	state, err := a.Run(context.Background(), "tell me")
	if err == nil || !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Fatalf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.FinalOutput != "" {
		t.Errorf("FinalOutput should be scrubbed on output block; got %q", state.FinalOutput)
	}
}
