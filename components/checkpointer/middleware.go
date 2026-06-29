package checkpointer

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

type component struct {
	c  Checkpointer
	id string
}

// New returns a Component that wires a Checkpointer (keyed by id) into the agent.
// It installs PhaseEnd middleware that calls Save with the supplied id. Save errors
// are wrapped as ErrCheckpointFailed and recorded on state.Trace but do not abort
// the run.
func New(c Checkpointer, id string) gantry.Component { return &component{c: c, id: id} }

func (comp *component) Install(a *gantry.Agent) error {
	const name = "components/checkpointer:save"
	return a.UseNamed(gantry.PhaseEnd, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := comp.c.Save(ctx, comp.id, s); err != nil {
				wrapped := fmt.Errorf("%w: %v", gantry.ErrCheckpointFailed, err)
				if s.Trace != nil {
					s.Trace.Record(gantry.TraceEvent{
						Name:  "checkpoint_failed",
						Kind:  gantry.KindEvent,
						Err:   wrapped,
						Attrs: map[string]any{"id": comp.id},
					})
				}
				// Non-fatal per spec.
				return nil
			}
			return nil
		}
	})
}
