// Package planner defines the Planner interface and a custom PhasePlan
// inserted between PhaseStart and PhaseAssembleContext.
package planner

import (
	"context"

	"github.com/farazhassan/gantry"
)

// PhasePlan is the custom phase the WithPlanner middleware registers
// (PositionAfter PhaseStart). It runs only once per agent run.
const PhasePlan gantry.Phase = "plan"

// Planner decomposes a task into a Plan.
type Planner interface {
	Plan(ctx context.Context, task string) (*gantry.Plan, error)
}
