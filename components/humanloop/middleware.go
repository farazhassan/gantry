package humanloop

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

type component struct{ h HumanInLoop }

// New returns a gantry.Component that installs PhaseToolExec middleware which
// calls Confirm for each pending tool call. If any decision is not approved,
// it sets state.Done with DoneHumanAborted and returns ErrHumanAborted.
func New(h HumanInLoop) gantry.Component {
	return &component{h: h}
}

func (c *component) Install(a *gantry.Agent) error {
	const name = "components/humanloop:confirm"
	return a.UseNamed(gantry.PhaseToolExec, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			for _, call := range s.PendingToolCalls {
				d, err := c.h.Confirm(ctx, Action{Kind: "tool", Name: call.Name, Args: call.Input})
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
