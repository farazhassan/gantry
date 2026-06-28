package limiter_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
)

func TestWithLimiterStopsLoopWhenExceeded(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "first", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 200}},
		gantry.LLMResponse{Content: "second", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 50}},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 100}))); err != nil {
		t.Fatalf("install limiter: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneBudgetExceeded {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneBudgetExceeded)
	}
}

func TestWithLimiterAllowsRunUnderLimit(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "ok",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 10},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 1000}))); err != nil {
		t.Fatalf("install limiter: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
}
