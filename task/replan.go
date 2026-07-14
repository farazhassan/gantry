package task

import (
	"context"
	"fmt"
	"strings"

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

// replan asks the Replanner to revise the ledger, appending the returned
// plan's new steps (adoptOrFlush → plan 12's relaxed Flush; the proposal's
// steps carry no ids, so they append to the tail with freshly minted
// continuing "s<N>" ids) and recording a critic-authored
// "Plan revised" note in Working. It reports whether the ledger was revised.
// Degrade contract: a nil replanner, a Replanner error, or a nil/empty
// proposal all return false and change nothing — the caller's critique hint
// (or plain continuation) stands alone, and the task NEVER fails because
// replanning did. On success the consecutive-rejection streak resets; the
// TotalRejections backstop keeps ratcheting, so a task that keeps getting
// rejected still parks at maxTotalRejections regardless of replans.
func (d *Driver) replan(ctx context.Context, t *Task, reason string) bool {
	if d.replanner == nil {
		return false
	}
	revised, err := d.replanner.Replan(ctx, t, reason)
	if err != nil || revised == nil || len(revised.Steps) == 0 {
		return false
	}
	before := 0
	if t.Plan != nil {
		before = len(t.Plan.Steps)
	}
	adoptOrFlush(t, revised)
	added := 0
	if t.Plan != nil {
		added = len(t.Plan.Steps) - before
	}
	// RoleUser, not RoleSystem, for the same reason the driver's rejection
	// critique uses it: providers have no mid-transcript system slot, and the
	// CriticAuthor tag keeps the note out of VisibleTranscript either way.
	t.Working = append(t.Working, gantry.Message{
		Role:    gantry.RoleUser,
		Name:    CriticAuthor,
		Content: fmt.Sprintf("Plan revised: %d new step(s) appended. Reason: %s\nFollow the updated plan.", added, reason),
	})
	t.ConsecutiveRejections = 0
	return true
}

// failedStepIDs returns the set of step IDs currently marked failed,
// snapshotted before a run so newlyFailedSteps can detect failures that
// happened DURING the run. Ledger steps always carry IDs (adopt/Flush
// mint them), so the ID is a reliable key.
func failedStepIDs(p *gantry.Plan) map[string]bool {
	if p == nil {
		return nil
	}
	ids := make(map[string]bool, len(p.Steps))
	for _, s := range p.Steps {
		if s.Status == gantry.StepFailed && s.ID != "" {
			ids[s.ID] = true
		}
	}
	return ids
}

// newlyFailedSteps returns the steps whose Status is failed now but was not
// before the run. A step's pending/active → failed transition happens in
// exactly one run, so each failure triggers at most one replan — an
// already-failed step can never re-trigger a replan loop.
func newlyFailedSteps(p *gantry.Plan, before map[string]bool) []gantry.PlanStep {
	if p == nil {
		return nil
	}
	var out []gantry.PlanStep
	for _, s := range p.Steps {
		if s.Status == gantry.StepFailed && !before[s.ID] {
			out = append(out, s)
		}
	}
	return out
}

// failedStepReason renders newly failed steps into the reason handed to the
// Replanner, e.g. `plan step failed: implement (build broke)`.
func failedStepReason(steps []gantry.PlanStep) string {
	var b strings.Builder
	b.WriteString("plan step failed: ")
	for i, s := range steps {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(s.Description)
		if s.Output != "" {
			b.WriteString(" (")
			b.WriteString(s.Output)
			b.WriteString(")")
		}
	}
	return b.String()
}
