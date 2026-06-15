// Package systemprompt provides a reusable building block that sets an
// agent's system prompt (its persona / base instructions) during context
// assembly.
package systemprompt

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// WithSystemPrompt installs a PhaseAssembleContext middleware that sets the
// run's system prompt. It sets state.System only on the first iteration and
// only when state.System is empty, so it establishes the base persona without
// clobbering values set by other middleware (e.g. skill) or carried forward
// across persisted turns. A blank prompt is a no-op (no middleware is
// registered). Re-registering returns the harness "already registered" error,
// which is ignored here — consistent with the skill component's pattern.
func WithSystemPrompt(a *harness.Agent, prompt string) {
	if prompt == "" {
		return
	}
	_ = a.UseNamed(harness.PhaseAssembleContext, "components/systemprompt",
		func(next harness.Handler) harness.Handler {
			return func(ctx context.Context, state *harness.State) error {
				if state.Iteration == 0 && state.System == "" {
					state.System = prompt
				}
				return next(ctx, state)
			}
		})
}
