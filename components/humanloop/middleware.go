package humanloop

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry/harness"
)

// WithHumanInLoop installs PhaseToolExec middleware that calls Confirm for
// each pending tool call. If any decision is not approved, sets state.Done
// with DoneHumanAborted and returns ErrHumanAborted.
func WithHumanInLoop(a *harness.Agent, h HumanInLoop) {
	const name = "components/humanloop:confirm"
	_ = a.UseNamed(harness.PhaseToolExec, name, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			for _, call := range s.PendingToolCalls {
				d, err := h.Confirm(ctx, Action{Kind: "tool", Name: call.Name, Args: call.Input})
				if err != nil {
					return err
				}
				if !d.Approved {
					s.Done = true
					s.DoneReason = harness.DoneHumanAborted
					return fmt.Errorf("%w: %s", harness.ErrHumanAborted, d.Reason)
				}
			}
			return next(ctx, s)
		}
	})
}
