package humanloop

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

// WithHumanInLoop installs PhaseToolExec middleware that calls Confirm for
// each pending tool call. If any decision is not approved, sets state.Done
// with DoneHumanAborted and returns ErrHumanAborted.
func WithHumanInLoop(a *gantry.Agent, h HumanInLoop) {
	const name = "components/humanloop:confirm"
	_ = a.UseNamed(gantry.PhaseToolExec, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			for _, call := range s.PendingToolCalls {
				d, err := h.Confirm(ctx, Action{Kind: "tool", Name: call.Name, Args: call.Input})
				if err != nil {
					return err
				}
				if !d.Approved {
					s.Done = true
					s.DoneReason = gantry.DoneHumanAborted
					return fmt.Errorf("%w: %s", gantry.ErrHumanAborted, d.Reason)
				}
			}
			return next(ctx, s)
		}
	})
}
