package compactor

import (
	"context"

	"github.com/farazhassan/gantry"
)

// WithCompactor installs PhaseAssembleContext middleware that compacts
// state.Messages to fit Budget. Register it AFTER WithMemory and
// WithRetriever so it is the outermost PhaseAssembleContext middleware; it
// runs the inner context-assembly middleware first (via next) and then
// compacts the fully-assembled transcript.
func WithCompactor(a *gantry.Agent, c Compactor, b Budget) {
	const name = "components/compactor:compact"
	_ = a.UseNamed(gantry.PhaseAssembleContext, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let inner context-assembly middleware populate s.Messages first,
			// then compact the result.
			if err := next(ctx, s); err != nil {
				return err
			}
			compacted, err := c.Compact(ctx, s.Messages, b)
			if err != nil {
				return err
			}
			s.Messages = compacted
			return nil
		}
	})
}
