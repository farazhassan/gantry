package taskmanager

import "github.com/farazhassan/gantry/task"

// DefaultMaxSpawnDepth is the spawn-tree depth cap applied when SpawnPolicy
// leaves MaxDepth unset (or non-positive): roots are depth 0, so children,
// grandchildren, and great-grandchildren are allowed; a depth-3 task may not
// spawn.
const DefaultMaxSpawnDepth = 3

// SpawnPolicy bounds what a running task's spawns may do. The zero value is a
// valid policy: default depth cap, children inherit the parent's budget limits.
type SpawnPolicy struct {
	// MaxDepth caps the spawn-tree depth. 0 (or negative) means the default
	// (DefaultMaxSpawnDepth). A spawn whose child would exceed the cap is
	// rejected at tool-Invoke time with a model-visible tool error; the run
	// continues.
	MaxDepth int
	// Budget derives a spawned child's cross-run budget from its parent. nil
	// means the child inherits a copy of the parent's budget LIMITS (MaxRuns,
	// MaxTokens, MaxCostUSD) with fresh usage counters.
	Budget func(parent *task.Task) task.TaskBudget
}

// WithSpawnPolicy sets the manager's spawn policy (depth cap + child budgets).
func WithSpawnPolicy(p SpawnPolicy) Option {
	return func(m *TaskManager) { m.policy = p }
}

// maxDepth resolves the effective depth cap, applying the default when unset.
func (p SpawnPolicy) maxDepth() int {
	if p.MaxDepth <= 0 {
		return DefaultMaxSpawnDepth
	}
	return p.MaxDepth
}

// childBudget derives a spawned child's budget from its parent per the policy:
// the custom Budget func when set, otherwise a copy of the parent's limits with
// zeroed usage.
func (m *TaskManager) childBudget(parent *task.Task) task.TaskBudget {
	if m.policy.Budget != nil {
		return m.policy.Budget(parent)
	}
	return task.TaskBudget{
		MaxRuns:    parent.Budget.MaxRuns,
		MaxTokens:  parent.Budget.MaxTokens,
		MaxCostUSD: parent.Budget.MaxCostUSD,
	}
}
