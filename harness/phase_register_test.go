package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRegisterPhaseAfterStart(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	const Custom harness.Phase = "custom_plan"
	if err := a.RegisterPhase(Custom, harness.PositionAfter, harness.PhaseStart); err != nil {
		t.Fatalf("RegisterPhase: %v", err)
	}

	var hit bool
	a.Use(Custom, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			hit = true
			return next(ctx, s)
		}
	})

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !hit {
		t.Errorf("custom phase middleware was not executed")
	}
}

func TestRegisterPhaseBeforeAssembleContext(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	const Custom harness.Phase = "before_assemble"
	if err := a.RegisterPhase(Custom, harness.PositionBefore, harness.PhaseAssembleContext); err != nil {
		t.Fatalf("RegisterPhase: %v", err)
	}

	calls := 0
	a.Use(Custom, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			calls++
			return next(ctx, s)
		}
	})

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected custom phase to run once per iteration; got %d", calls)
	}
}

func TestRegisterPhaseUnknownAnchorErrors(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	const Custom harness.Phase = "x"
	err := a.RegisterPhase(Custom, harness.PositionAfter, harness.Phase("ghost"))
	if err == nil {
		t.Errorf("expected error for unknown anchor")
	}
}

func TestRegisterPhaseDuplicateErrors(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	err := a.RegisterPhase(harness.PhaseLLMCall, harness.PositionAfter, harness.PhaseStart)
	if err == nil {
		t.Errorf("expected error for duplicate phase")
	}
}
