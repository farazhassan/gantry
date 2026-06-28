// Package systemprompt provides a reusable building block that sets an
// agent's system prompt (its persona / base instructions) during context
// assembly.
package systemprompt

import (
	"context"

	"github.com/farazhassan/gantry"
)

type component struct{ prompt string }

// New returns a Component that sets the agent's system prompt (persona / base
// instructions) during context assembly. It sets state.System only on iteration 0
// and only when state.System is empty, so it establishes the base persona without
// clobbering a value another component set earlier in the same turn (e.g. skill).
// A blank prompt is a no-op (no middleware is registered).
func New(prompt string) gantry.Component { return &component{prompt: prompt} }

func (c *component) Install(a *gantry.Agent) error {
	if c.prompt == "" {
		return nil
	}
	return a.UseNamed(gantry.PhaseAssembleContext, "components/systemprompt",
		func(next gantry.Handler) gantry.Handler {
			return func(ctx context.Context, state *gantry.State) error {
				if state.Iteration == 0 && state.System == "" {
					state.System = c.prompt
				}
				return next(ctx, state)
			}
		})
}
