package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestRegisterPhaseAfterStart(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	const Custom gantry.Phase = "custom_plan"
	if err := a.RegisterPhase(Custom, gantry.PositionAfter, gantry.PhaseStart); err != nil {
		t.Fatalf("RegisterPhase: %v", err)
	}

	var hit bool
	a.Use(Custom, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
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
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	const Custom gantry.Phase = "before_assemble"
	if err := a.RegisterPhase(Custom, gantry.PositionBefore, gantry.PhaseAssembleContext); err != nil {
		t.Fatalf("RegisterPhase: %v", err)
	}

	calls := 0
	a.Use(Custom, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
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
	a, _ := gantry.NewAgent(gantry.WithLLM(nilLLM{}))
	const Custom gantry.Phase = "x"
	err := a.RegisterPhase(Custom, gantry.PositionAfter, gantry.Phase("ghost"))
	if err == nil {
		t.Errorf("expected error for unknown anchor")
	}
}

func TestRegisterPhaseDuplicateErrors(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(nilLLM{}))
	err := a.RegisterPhase(gantry.PhaseLLMCall, gantry.PositionAfter, gantry.PhaseStart)
	if err == nil {
		t.Errorf("expected error for duplicate phase")
	}
}
