package guardrail

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// WithGuardrail installs the guard on both directions:
//   - PhaseLLMCall (innermost): pre-LLM input check
//   - PhasePostLLM: post-LLM output check
//
// Both shortcuts set state.Done = true and DoneReason = DoneGuardrailBlocked
// and return ErrGuardrailBlocked from Run.
func WithGuardrail(a *gantry.Agent, g Guardrail) {
	const inName = "components/guardrail:in"
	const outName = "components/guardrail:out"

	_ = a.UseNamed(gantry.PhaseLLMCall, inName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := g.Check(ctx, s, DirectionInput); err != nil {
				if errors.Is(err, gantry.ErrGuardrailBlocked) {
					s.Done = true
					s.DoneReason = gantry.DoneGuardrailBlocked
				}
				return err
			}
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(gantry.PhasePostLLM, outName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := g.Check(ctx, s, DirectionOutput); err != nil {
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
