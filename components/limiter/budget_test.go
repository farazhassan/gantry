package limiter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/harness"
)

func TestBudgetLimiterUnderLimit(t *testing.T) {
	l := limiter.NewBudget(limiter.Limits{MaxTokens: 1000})
	state := &harness.State{Usage: harness.Usage{InputTokens: 100, OutputTokens: 50}}
	if err := l.Check(context.Background(), state); err != nil {
		t.Errorf("Check should pass under limit; got %v", err)
	}
}

func TestBudgetLimiterOverTokenLimit(t *testing.T) {
	l := limiter.NewBudget(limiter.Limits{MaxTokens: 100})
	state := &harness.State{Usage: harness.Usage{InputTokens: 80, OutputTokens: 30}}
	err := l.Check(context.Background(), state)
	if !errors.Is(err, harness.ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded; got %v", err)
	}
}

func TestBudgetLimiterOverCostLimit(t *testing.T) {
	l := limiter.NewBudget(limiter.Limits{MaxCostUSD: 0.5})
	state := &harness.State{Usage: harness.Usage{Cost: 0.75}}
	err := l.Check(context.Background(), state)
	if !errors.Is(err, harness.ErrLimitExceeded) {
		t.Errorf("expected ErrLimitExceeded; got %v", err)
	}
}

func TestBudgetLimiterRecordAccumulates(t *testing.T) {
	l := limiter.NewBudget(limiter.Limits{})
	l.Record(context.Background(), harness.Usage{InputTokens: 10})
	l.Record(context.Background(), harness.Usage{InputTokens: 20})
	if got := l.Total(); got.InputTokens != 30 {
		t.Errorf("Total.InputTokens = %d, want 30", got.InputTokens)
	}
}

func TestLimiterInterface(t *testing.T) {
	var _ limiter.Limiter = limiter.NewBudget(limiter.Limits{})
}
