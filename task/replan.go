package task

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Replanner revises a task's plan mid-flight. It is the replan seam the
// Driver invokes when execution signals the current plan is not working: a
// verifier-rejection streak one short of the consecutive cap, or a plan step
// that newly failed during a run. reason carries the trigger's diagnosis (the
// rejection reason, or the failed step rendered by failedStepReason). The
// returned plan's NEW steps are appended to the task's ledger (adoptOrFlush,
// whose relaxed Flush adopts id-less steps at the tail); existing steps and
// their statuses are preserved. Implementations live
// outside this package (see components/planner); package task defines only
// the seam.
type Replanner interface {
	Replan(ctx context.Context, t *Task, reason string) (*gantry.Plan, error)
}

// WithReplanner wires a Replanner into the Driver. A nil replanner is ignored
// (the default is kept: no replanning; rejections inject critique hints only).
func WithReplanner(r Replanner) Option {
	return func(d *Driver) {
		if r != nil {
			d.replanner = r
		}
	}
}
