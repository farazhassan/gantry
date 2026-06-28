package limiter

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

type component struct{ l Limiter }

// New returns a Component that installs token-budget middleware: a PhaseLLMCall
// pre-check, a PhasePostLLM usage recorder, and a PhasePostLLM finalize that
// terminates the loop when the limit is exceeded. Register limiter before memory
// so memory:persist observes the finalized turn (see package doc).
//
// When the limit is exceeded mid-run, state.Done is set with
// DoneBudgetExceeded. The current iteration's response is still appended.
//
// Middleware ordering: among the PhasePostLLM steps, record does its work
// before next() (pre-next), while finalize does its work after next()
// (post-next). Pre-next work runs in reverse registration order and post-next
// work in forward order (last-registered = outermost = runs last). Register
// WithMemory after limiter and critic so memory:persist observes the
// finalized turn. See the memory package's "Middleware ordering" note.
func New(l Limiter) gantry.Component { return &component{l: l} }

func (c *component) Install(a *gantry.Agent) error {
	const checkName = "components/limiter:check"
	const recordName = "components/limiter:record"
	const finalizeName = "components/limiter:finalize"

	if err := a.UseNamed(gantry.PhaseLLMCall, checkName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := c.l.Check(ctx, s); err != nil {
				if errors.Is(err, gantry.ErrLimitExceeded) {
					s.Done = true
					s.DoneReason = gantry.DoneBudgetExceeded
					return nil
				}
				return err
			}
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	if err := a.UseNamed(gantry.PhasePostLLM, recordName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if s.LastResponse != nil {
				c.l.Record(ctx, s.LastResponse.Usage)
			}
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhasePostLLM, finalizeName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := c.l.Check(ctx, s); err != nil && errors.Is(err, gantry.ErrLimitExceeded) {
				s.Done = true
				s.DoneReason = gantry.DoneBudgetExceeded
			}
			return nil
		}
	})
}
