package limiter

import (
	"context"
	"fmt"
	"sync"

	"github.com/farazhassan/gantry"
)

// Limits configures BudgetLimiter. A zero value disables that bound.
type Limits struct {
	MaxTokens  int     // input + output combined
	MaxCostUSD float64 // 0 = unlimited
}

// BudgetLimiter enforces token and cost ceilings.
type BudgetLimiter struct {
	limits Limits
	mu     sync.Mutex
	total  gantry.Usage
}

// NewBudget returns a BudgetLimiter with the given limits.
func NewBudget(l Limits) *BudgetLimiter {
	return &BudgetLimiter{limits: l}
}

// Check inspects state.Usage; returns ErrLimitExceeded if any configured
// bound has been crossed.
func (b *BudgetLimiter) Check(_ context.Context, state *gantry.State) error {
	u := state.Usage
	if b.limits.MaxTokens > 0 && (u.InputTokens+u.OutputTokens) > b.limits.MaxTokens {
		return fmt.Errorf("%w: tokens %d > %d", gantry.ErrLimitExceeded, u.InputTokens+u.OutputTokens, b.limits.MaxTokens)
	}
	if b.limits.MaxCostUSD > 0 && u.Cost > b.limits.MaxCostUSD {
		return fmt.Errorf("%w: cost %.4f > %.4f", gantry.ErrLimitExceeded, u.Cost, b.limits.MaxCostUSD)
	}
	return nil
}

// Record accumulates total usage seen across all calls.
func (b *BudgetLimiter) Record(_ context.Context, u gantry.Usage) {
	b.mu.Lock()
	b.total = b.total.Add(u)
	b.mu.Unlock()
}

// Total returns the accumulated usage (for inspection / tests).
func (b *BudgetLimiter) Total() gantry.Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}
