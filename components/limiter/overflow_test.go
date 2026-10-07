package limiter_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
)

func TestLimiterRecordsUsageOfOverflowedAttempt(t *testing.T) {
	cut := gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: "t1", Name: "noop", Input: []byte(`{"q":`)}},
		StopReason: gantry.StopReasonContextWindow,
		Usage:      gantry.Usage{InputTokens: 90, OutputTokens: 10},
	}
	ok := gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 5, OutputTokens: 5}}
	mock := eval.NewMockLLMClient(cut, ok)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	b := limiter.NewBudget(limiter.Limits{MaxTokens: 1_000_000})
	_ = a.With(limiter.New(b))
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { return true, nil })

	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 95, OutputTokens: 15}
	if got := b.Total(); got != want {
		t.Errorf("limiter Total = %+v, want %+v (both attempts, once each)", got, want)
	}
	if s.Usage != want {
		t.Errorf("state Usage = %+v, want %+v", s.Usage, want)
	}
}
