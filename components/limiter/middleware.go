package limiter

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// WithLimiter installs middleware on PhaseLLMCall (pre-check + post-record)
// and PhasePostLLM (terminate the loop if limit exceeded).
//
// When the limit is exceeded mid-run, state.Done is set with
// DoneBudgetExceeded. The current iteration's response is still appended.
//
// Middleware ordering: among the PhasePostLLM steps, record does its work
// before next() (pre-next), while finalize does its work after next()
// (post-next). Pre-next work runs in reverse registration order and post-next
// work in forward order (last-registered = outermost = runs last). Register
// WithMemory after WithLimiter and WithCritic so memory:persist observes the
// finalized turn. See the memory package's "Middleware ordering" note.
func WithLimiter(a *gantry.Agent, l Limiter) {
	const checkName = "components/limiter:check"
	const recordName = "components/limiter:record"
	const finalizeName = "components/limiter:finalize"

	_ = a.UseNamed(gantry.PhaseLLMCall, checkName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := l.Check(ctx, s); err != nil {
				if errors.Is(err, gantry.ErrLimitExceeded) {
					s.Done = true
					s.DoneReason = gantry.DoneBudgetExceeded
					return nil
				}
				return err
			}
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(gantry.PhasePostLLM, recordName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if s.LastResponse != nil {
				l.Record(ctx, s.LastResponse.Usage)
			}
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(gantry.PhasePostLLM, finalizeName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := l.Check(ctx, s); err != nil && errors.Is(err, gantry.ErrLimitExceeded) {
				s.Done = true
				s.DoneReason = gantry.DoneBudgetExceeded
			}
			return nil
		}
	})
}
