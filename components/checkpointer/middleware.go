package checkpointer

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry/harness"
)

// WithCheckpointer installs PhaseEnd middleware that calls Save with the
// supplied id. Save errors are wrapped as ErrCheckpointFailed and recorded
// on state.Trace but do not abort the run.
func WithCheckpointer(a *harness.Agent, c Checkpointer, id string) {
	const name = "components/checkpointer:save"
	_ = a.UseNamed(harness.PhaseEnd, name, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := c.Save(ctx, id, s); err != nil {
				wrapped := fmt.Errorf("%w: %v", harness.ErrCheckpointFailed, err)
				if s.Trace != nil {
					s.Trace.Record(harness.TraceEvent{
						Name:  "checkpoint_failed",
						Kind:  harness.KindEvent,
						Err:   wrapped,
						Attrs: map[string]any{"id": id},
					})
				}
				// Non-fatal per spec.
				return nil
			}
			return nil
		}
	})
}
