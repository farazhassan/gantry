package skill

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry"
)

type component struct{ s Skill }

// New returns a gantry.Component that installs a PhaseAssembleContext middleware
// which appends the skill's prompt to state.System when Matches returns true.
//
// The prompt is appended only on the first iteration. PhaseAssembleContext
// re-runs every iteration and state.System persists, so appending every
// iteration would stack duplicate prompts.
//
// Multiple skills can be registered; their prompts are joined by newlines
// in registration order.
func New(s Skill) gantry.Component {
	return &component{s: s}
}

func (c *component) Install(a *gantry.Agent) error {
	name := "components/skill:" + c.s.Name()
	_ = a.UseNamed(gantry.PhaseAssembleContext, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, state *gantry.State) error {
			if state.Iteration == 0 && c.s.Matches(ctx, state) {
				if state.System != "" {
					state.System = strings.TrimRight(state.System, "\n") + "\n" + c.s.SystemPrompt()
				} else {
					state.System = c.s.SystemPrompt()
				}
			}
			return next(ctx, state)
		}
	})
	return nil
}
