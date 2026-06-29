package task

import "github.com/farazhassan/gantry"

// exceeded reports whether the task has hit any of its cross-run limits. A zero
// limit means "unlimited" for that dimension. Tokens are the sum of input and
// output tokens accumulated across runs.
func (b *TaskBudget) exceeded() bool {
	if b.MaxRuns > 0 && b.UsedRuns >= b.MaxRuns {
		return true
	}
	if b.MaxTokens > 0 && b.UsedUsage.InputTokens+b.UsedUsage.OutputTokens >= b.MaxTokens {
		return true
	}
	if b.MaxCostUSD > 0 && b.UsedUsage.Cost >= b.MaxCostUSD {
		return true
	}
	return false
}

// recordRun accounts for one completed run: it increments the run count and adds
// the run's usage to the cumulative total.
func (b *TaskBudget) recordRun(u gantry.Usage) {
	b.UsedRuns++
	b.UsedUsage = b.UsedUsage.Add(u)
}
