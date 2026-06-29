package guardrail

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

type component struct{ g Guardrail }

// New returns a gantry.Component that installs the guard on both directions:
//   - PhaseLLMCall (innermost): pre-LLM input check
//   - PhasePostLLM: post-LLM output check
//
// Both shortcuts set state.Done = true and DoneReason = DoneGuardrailBlocked
// and return ErrGuardrailBlocked from Run.
func New(g Guardrail) gantry.Component {
	return &component{g: g}
}

func (c *component) Install(a *gantry.Agent) error {
	const inName = "components/guardrail:in"
	const outName = "components/guardrail:out"

	if err := a.UseNamed(gantry.PhaseLLMCall, inName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := c.g.Check(ctx, s, DirectionInput); err != nil {
				if errors.Is(err, gantry.ErrGuardrailBlocked) {
					s.Done = true
					s.DoneReason = gantry.DoneGuardrailBlocked
				}
				return err
			}
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhasePostLLM, outName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := c.g.Check(ctx, s, DirectionOutput); err != nil {
				if errors.Is(err, gantry.ErrGuardrailBlocked) {
					s.Done = true
					s.DoneReason = gantry.DoneGuardrailBlocked
					// Scrub any final output DefaultPostLLMHandler already set,
					// so a caller that ignores the returned error cannot read
					// blocked content out of FinalOutput.
					s.FinalOutput = ""
				}
				return err
			}
			return nil
		}
	})
}
