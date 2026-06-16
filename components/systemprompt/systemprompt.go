// Package systemprompt provides a reusable building block that sets an
// agent's system prompt (its persona / base instructions) during context
// assembly.
package systemprompt

import (
	"context"

	"github.com/farazhassan/gantry"
)

// WithSystemPrompt installs a PhaseAssembleContext middleware that sets the
// run's system prompt. It sets state.System only on iteration 0 (once per
// turn, not on every tool-call loop) and only when state.System is empty, so
// it establishes the base persona without clobbering a value another
// middleware set earlier in the same turn (e.g. skill). A resumed turn begins
// with an empty System, so the persona is re-applied each turn and editing the
// prompt takes effect on the next turn. A blank prompt is a no-op (no
// middleware is registered). Re-registering returns the harness "already
// registered" error, which is ignored here — consistent with the skill
// component's pattern.
func WithSystemPrompt(a *gantry.Agent, prompt string) {
	if prompt == "" {
		return
	}
	_ = a.UseNamed(gantry.PhaseAssembleContext, "components/systemprompt",
		func(next gantry.Handler) gantry.Handler {
			return func(ctx context.Context, state *gantry.State) error {
				if state.Iteration == 0 && state.System == "" {
					state.System = prompt
				}
				return next(ctx, state)
			}
		})
}
