package guardrail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/eval"
)

func TestWithGuardrailBlocksOnInputMatch(t *testing.T) {
	mock := eval.NewMockLLMClient() // should not be called
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(guardrail.New(guardrail.NewRegex(`(?i)password`, guardrail.DirectionInput))); err != nil {
		t.Fatalf("install guardrail: %v", err)
	}

	state, err := a.Run(context.Background(), "what is my password")
	if err == nil || !errors.Is(err, gantry.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.DoneReason != gantry.DoneGuardrailBlocked {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("LLM should not be called; got %d requests", len(mock.Requests()))
	}
}

func TestWithGuardrailBlocksOnOutputMatch(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "the secret is 42", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(guardrail.New(guardrail.NewRegex(`(?i)secret`, guardrail.DirectionOutput))); err != nil {
		t.Fatalf("install guardrail: %v", err)
	}

	state, err := a.Run(context.Background(), "tell me")
	if err == nil || !errors.Is(err, gantry.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.DoneReason != gantry.DoneGuardrailBlocked {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
}

func TestWithGuardrailOutputBlockScrubsFinalOutput(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "the secret is 42", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(guardrail.New(guardrail.NewRegex(`(?i)secret`, guardrail.DirectionOutput))); err != nil {
		t.Fatalf("install guardrail: %v", err)
	}

	state, err := a.Run(context.Background(), "tell me")
	if err == nil || !errors.Is(err, gantry.ErrGuardrailBlocked) {
		t.Fatalf("expected ErrGuardrailBlocked; got %v", err)
	}
	if state.FinalOutput != "" {
		t.Errorf("FinalOutput should be scrubbed on output block; got %q", state.FinalOutput)
	}
}
