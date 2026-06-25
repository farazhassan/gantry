// Package task defines the durable Task entity that sits above the Session
// conversation layer: a keyed work item with a plan-ledger, a status machine, a
// cross-run budget, and its own working context. Plan 1 ships the data model,
// the TaskStore, and the hydrate/flush ledger projection; the task driver that
// orchestrates runs is a later plan.
package task

import (
	"time"

	"github.com/farazhassan/gantry"
)

// TaskStatus is the lifecycle state of a Task.
//
//	pending -> active -> awaiting_input -> done   (explicit, verifier-gated)
//	                  \-> failed / cancelled
//
// pending doubles as the queued state. done/failed/cancelled are terminal.
type TaskStatus string

const (
	TaskPending       TaskStatus = "pending"        // created/queued; no plan or not started
	TaskActive        TaskStatus = "active"         // has >=1 step; executing
	TaskAwaitingInput TaskStatus = "awaiting_input" // suspended for user input
	TaskDone          TaskStatus = "done"           // explicit, verifier-gated
	TaskFailed        TaskStatus = "failed"         // terminal; execution error
	TaskCancelled     TaskStatus = "cancelled"      // terminal; explicitly stopped
)

// IsTerminal reports whether the status is a final resting state (no further
// runs will be launched for the task).
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskDone, TaskFailed, TaskCancelled:
		return true
	default:
		return false
	}
}

// TaskBudget caps a task's work across many runs (distinct from the per-run
// maxIterations cap). A zero limit means unlimited.
type TaskBudget struct {
	MaxRuns    int
	MaxTokens  int
	MaxCostUSD float64
	UsedRuns   int
	UsedUsage  gantry.Usage
}

// Task is the durable work item, stored by ID in a TaskStore. It is
// origin-agnostic: it records neither who created it (agent/user/scheduler) nor
// any schedule time — those are creation/orchestration concerns.
type Task struct {
	ID        string
	SessionID string // ID of the session that created this task
	Title     string
	Goal      string
	Status    TaskStatus
	Plan      *gantry.Plan      // the ledger — source of truth for progress
	Budget    TaskBudget        // cross-run budget
	Working   []gantry.Message  // task's own working context, separate from the chat transcript
	Pending   []gantry.ToolCall // unfulfilled ask_user call(s); set only while Status == TaskAwaitingInput
	// ConsecutiveRejections counts critic rejections in the current done cycle.
	// It is reset to 0 on any non-reject outcome and bounds how many times the
	// driver will re-prompt after a rejection before failing the task.
	ConsecutiveRejections int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// TaskRef is the lightweight reference a Session keeps for each task it owns;
// the heavy Task state lives in the TaskStore.
type TaskRef struct {
	ID        string
	Title     string
	Status    TaskStatus
	CreatedAt time.Time
}

// SessionMeta is the session-side link to its tasks: an ordered, append-only
// history of refs plus the id of the single active task ("" when none).
type SessionMeta struct {
	TaskRefs     []TaskRef
	ActiveTaskID string
}
