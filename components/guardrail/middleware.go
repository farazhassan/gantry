package guardrail

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry/harness"
)

// WithGuardrail installs the guard on both directions:
//   - PhaseLLMCall (innermost): pre-LLM input check
//   - PhasePostLLM: post-LLM output check
//
// Both shortcuts set state.Done = true and DoneReason = DoneGuardrailBlocked
// and return ErrGuardrailBlocked from Run.
func WithGuardrail(a *harness.Agent, g Guardrail) {
	const inName = "components/guardrail:in"
	const outName = "components/guardrail:out"

	_ = a.UseNamed(harness.PhaseLLMCall, inName, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			if err := g.Check(ctx, s, DirectionInput); err != nil {
				if errors.Is(err, harness.ErrGuardrailBlocked) {
					s.Done = true
					s.DoneReason = harness.DoneGuardrailBlocked
				}
				return err
			}
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(harness.PhasePostLLM, outName, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := g.Check(ctx, s, DirectionOutput); err != nil {
				if errors.Is(err, harness.ErrGuardrailBlocked) {
					s.Done = true
					s.DoneReason = harness.DoneGuardrailBlocked
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
