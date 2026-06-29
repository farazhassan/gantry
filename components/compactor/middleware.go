package compactor

import (
	"context"

	"github.com/farazhassan/gantry"
)

type component struct {
	c Compactor
	b Budget
}

// New returns a Component that wires a Compactor with the given Budget into the
// agent. Install it AFTER memory.New and retriever.New so it is the outermost
// PhaseAssembleContext middleware; it runs the inner context-assembly middleware
// first (via next) and then compacts the fully-assembled transcript.
func New(c Compactor, b Budget) gantry.Component { return &component{c: c, b: b} }

func (comp *component) Install(a *gantry.Agent) error {
	const name = "components/compactor:compact"
	return a.UseNamed(gantry.PhaseAssembleContext, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let inner context-assembly middleware populate s.Messages first,
			// then compact the result.
			if err := next(ctx, s); err != nil {
				return err
			}
			compacted, err := comp.c.Compact(ctx, s.Messages, comp.b)
			if err != nil {
				return err
			}
			s.Messages = compacted
			return nil
		}
	})
}
