package limiter_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithLimiterStopsLoopWhenExceeded(t *testing.T) {
	mock := eval.NewMockLLMClient(
		harness.LLMResponse{Content: "first", StopReason: harness.StopReasonEnd, Usage: harness.Usage{InputTokens: 200}},
		harness.LLMResponse{Content: "second", StopReason: harness.StopReasonEnd, Usage: harness.Usage{InputTokens: 50}},
	)
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	limiter.WithLimiter(a, limiter.NewBudget(limiter.Limits{MaxTokens: 100}))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != harness.DoneBudgetExceeded {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneBudgetExceeded)
	}
}

func TestWithLimiterAllowsRunUnderLimit(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{
		Content:    "ok",
		StopReason: harness.StopReasonEnd,
		Usage:      harness.Usage{InputTokens: 10},
	})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	limiter.WithLimiter(a, limiter.NewBudget(limiter.Limits{MaxTokens: 1000}))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneNoToolCalls)
	}
}
