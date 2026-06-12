package skill

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry/harness"
)

// WithSkill installs a PhaseAssembleContext middleware that appends the
// skill's prompt to state.System when Matches returns true.
//
// The prompt is appended only on the first iteration. PhaseAssembleContext
// re-runs every iteration and state.System persists, so appending every
// iteration would stack duplicate prompts.
//
// Multiple skills can be registered; their prompts are joined by newlines
// in registration order.
func WithSkill(a *harness.Agent, s Skill) {
	name := "components/skill:" + s.Name()
	_ = a.UseNamed(harness.PhaseAssembleContext, name, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, state *harness.State) error {
			if state.Iteration == 0 && s.Matches(ctx, state) {
				if state.System != "" {
					state.System = strings.TrimRight(state.System, "\n") + "\n" + s.SystemPrompt()
				} else {
					state.System = s.SystemPrompt()
				}
			}
			return next(ctx, state)
		}
	})
}
