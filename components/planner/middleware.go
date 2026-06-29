package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

type component struct{ p Planner }

// New returns a Component that registers a PhasePlan after PhaseStart and installs
// middleware that calls Planner.Plan and injects the resulting plan into
// state.System during context assembly.
func New(p Planner) gantry.Component { return &component{p: p} }

func (c *component) Install(a *gantry.Agent) error {
	if err := a.RegisterPhase(PhasePlan, gantry.PositionAfter, gantry.PhaseStart); err != nil {
		// If already registered (e.g. by another planner.New call), continue.
		if !strings.Contains(err.Error(), "already registered") {
			return err
		}
	}

	const planName = "components/planner:plan"
	if err := a.UseNamed(PhasePlan, planName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if s.Iteration > 0 || s.Plan != nil {
				return next(ctx, s)
			}
			task := s.Task
			if task == "" {
				task = s.Input
			}
			plan, err := c.p.Plan(ctx, task)
			if err != nil {
				return err
			}
			s.Plan = plan
			s.Task = task
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	const injectName = "components/planner:inject"
	return a.UseNamed(gantry.PhaseAssembleContext, injectName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Inject the plan only on the first iteration. PhaseAssembleContext
			// re-runs every iteration and s.System persists, so appending every
			// iteration would stack duplicate "Plan:" blocks.
			if s.Iteration == 0 && s.Plan != nil && len(s.Plan.Steps) > 0 {
				var b strings.Builder
				b.WriteString("\n\nPlan:\n")
				for i, step := range s.Plan.Steps {
					fmt.Fprintf(&b, "%d. %s\n", i+1, step.Description)
				}
				s.System += b.String()
			}
			return next(ctx, s)
		}
	})
}
