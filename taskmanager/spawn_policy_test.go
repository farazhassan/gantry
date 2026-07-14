package taskmanager

import (
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

func TestSpawnPolicyMaxDepthDefaults(t *testing.T) {
	if got := (SpawnPolicy{}).maxDepth(); got != DefaultMaxSpawnDepth {
		t.Errorf("zero policy maxDepth = %d, want %d", got, DefaultMaxSpawnDepth)
	}
	if got := (SpawnPolicy{MaxDepth: 5}).maxDepth(); got != 5 {
		t.Errorf("MaxDepth 5 maxDepth = %d, want 5", got)
	}
	if got := (SpawnPolicy{MaxDepth: -1}).maxDepth(); got != DefaultMaxSpawnDepth {
		t.Errorf("negative MaxDepth maxDepth = %d, want %d", got, DefaultMaxSpawnDepth)
	}
}

func TestChildBudgetInheritsLimitsNotUsage(t *testing.T) {
	// nil Budget func: the child gets a copy of the parent's LIMITS with zeroed
	// usage counters.
	tm, _, _ := newManager(&scriptedRunner{})
	parent := &task.Task{Budget: task.TaskBudget{
		MaxRuns:    7,
		MaxTokens:  1000,
		MaxCostUSD: 2.5,
		UsedRuns:   4,
		UsedUsage:  gantry.Usage{InputTokens: 100, OutputTokens: 50, Cost: 1.2},
	}}
	got := tm.childBudget(parent)
	want := task.TaskBudget{MaxRuns: 7, MaxTokens: 1000, MaxCostUSD: 2.5}
	if got != want {
		t.Errorf("childBudget = %+v, want %+v (limits copied, usage zeroed)", got, want)
	}
}

func TestChildBudgetCustomFunc(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(&scriptedRunner{}, tasks)
	tm := NewTaskManager(driver, tasks, NewInMemoryMetaStore(), NewInMemoryReadyQueue(),
		WithSpawnPolicy(SpawnPolicy{
			Budget: func(parent *task.Task) task.TaskBudget {
				return task.TaskBudget{MaxRuns: parent.Budget.MaxRuns / 2}
			},
		}),
	)
	parent := &task.Task{Budget: task.TaskBudget{MaxRuns: 8}}
	if got := tm.childBudget(parent); got.MaxRuns != 4 {
		t.Errorf("childBudget.MaxRuns = %d, want 4 (custom func applied)", got.MaxRuns)
	}
}
