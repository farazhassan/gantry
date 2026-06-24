package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestBudgetExceededZeroIsUnlimited(t *testing.T) {
	var b TaskBudget // all limits zero
	b.UsedRuns = 1000
	b.UsedUsage = gantry.Usage{InputTokens: 1 << 20, OutputTokens: 1 << 20, Cost: 9999}
	if b.exceeded() {
		t.Error("zero limits must mean unlimited, but exceeded() returned true")
	}
}

func TestBudgetExceededByRuns(t *testing.T) {
	b := TaskBudget{MaxRuns: 2}
	b.UsedRuns = 1
	if b.exceeded() {
		t.Error("1 run used of 2 must not be exceeded")
	}
	b.UsedRuns = 2
	if !b.exceeded() {
		t.Error("2 runs used of 2 must be exceeded")
	}
}

func TestBudgetExceededByTokens(t *testing.T) {
	b := TaskBudget{MaxTokens: 100}
	b.UsedUsage = gantry.Usage{InputTokens: 40, OutputTokens: 40} // 80 total
	if b.exceeded() {
		t.Error("80 tokens of 100 must not be exceeded")
	}
	b.UsedUsage = gantry.Usage{InputTokens: 60, OutputTokens: 40} // 100 total
	if !b.exceeded() {
		t.Error("100 tokens of 100 must be exceeded")
	}
}

func TestBudgetExceededByCost(t *testing.T) {
	b := TaskBudget{MaxCostUSD: 1.0}
	b.UsedUsage = gantry.Usage{Cost: 0.99}
	if b.exceeded() {
		t.Error("0.99 of 1.00 must not be exceeded")
	}
	b.UsedUsage = gantry.Usage{Cost: 1.0}
	if !b.exceeded() {
		t.Error("1.00 of 1.00 must be exceeded")
	}
}

func TestBudgetRecordRun(t *testing.T) {
	var b TaskBudget
	b.recordRun(gantry.Usage{InputTokens: 10, OutputTokens: 5, Cost: 0.1})
	b.recordRun(gantry.Usage{InputTokens: 1, OutputTokens: 2, Cost: 0.2})
	if b.UsedRuns != 2 {
		t.Errorf("UsedRuns = %d, want 2", b.UsedRuns)
	}
	if b.UsedUsage.InputTokens != 11 || b.UsedUsage.OutputTokens != 7 {
		t.Errorf("tokens = %d/%d, want 11/7", b.UsedUsage.InputTokens, b.UsedUsage.OutputTokens)
	}
	if b.UsedUsage.Cost < 0.299 || b.UsedUsage.Cost > 0.301 {
		t.Errorf("cost = %v, want ~0.3", b.UsedUsage.Cost)
	}
}
