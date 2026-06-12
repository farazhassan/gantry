package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/planner"
)

// PlannerSuite verifies the contract of planner.Planner.
func PlannerSuite(t *testing.T, factory func() planner.Planner) {
	t.Helper()

	t.Run("plan_returns_non_nil_or_error", func(t *testing.T) {
		p := factory()
		plan, err := p.Plan(context.Background(), "do something")
		if err != nil {
			return
		}
		if plan == nil {
			t.Errorf("Plan returned nil plan with no error")
		}
	})

	t.Run("plan_propagates_task_in_goal", func(t *testing.T) {
		p := factory()
		plan, err := p.Plan(context.Background(), "specific-task")
		if err != nil || plan == nil {
			return
		}
		if plan.Goal != "specific-task" {
			t.Errorf("plan.Goal = %q, want %q", plan.Goal, "specific-task")
		}
	})
}
