package compactor

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

// MetaOverflowRetries is the State.Meta key holding how many times the
// overflow retry compacted and re-sent a request this run (an int).
const MetaOverflowRetries = "components/compactor:overflow_retries"

// overflowFallbackPercent is the target, as a percentage of the current
// prompt estimate, when neither the provider error nor State.ContextWindow
// gives a limit.
const overflowFallbackPercent = 75

type component struct {
	c Compactor
	b Budget
}

// New returns a Component that wires a Compactor with the given Budget into the
// agent. Install it AFTER transcript.New and retriever.New so it is the outermost
// PhaseAssembleContext middleware; it runs the inner context-assembly middleware
// first (via next) and then compacts the fully-assembled transcript.
//
// It also installs a PhaseLLMCall middleware: when the LLM call fails with
// gantry.ErrContextLengthExceeded, it compacts once more with Budget.Force set
// and MaxTokens set to the provider-reported limit (else State.ContextWindow,
// else 75% of EstimatePromptTokens), then retries the call exactly once.
func New(c Compactor, b Budget) gantry.Component { return &component{c: c, b: b} }

func (comp *component) Install(a *gantry.Agent) error {
	if err := a.UseNamed(gantry.PhaseAssembleContext, "components/compactor:compact", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let inner context-assembly middleware populate s.Messages first,
			// then compact the result.
			if err := next(ctx, s); err != nil {
				return err
			}
			before := len(s.Messages)
			compacted, err := comp.c.Compact(ctx, s.Messages, comp.b)
			if err != nil {
				return err
			}
			s.Messages = compacted
			if len(compacted) != before {
				// The measured anchor no longer describes Messages.
				s.ContextUsage = gantry.ContextUsage{}
			}
			return nil
		}
	}); err != nil {
		return err
	}
	return a.UseNamed(gantry.PhaseLLMCall, "components/compactor:overflow_retry", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			err := next(ctx, s)
			if err == nil || !errors.Is(err, gantry.ErrContextLengthExceeded) {
				return err
			}
			b := comp.b
			b.Force = true
			b.MaxTokens = comp.overflowTarget(s, err)
			compacted, cerr := comp.c.Compact(ctx, s.Messages, b)
			if cerr != nil {
				return errors.Join(err, cerr)
			}
			s.Messages = compacted
			s.ContextUsage = gantry.ContextUsage{}
			if s.Meta == nil {
				s.Meta = map[string]any{}
			}
			n, _ := s.Meta[MetaOverflowRetries].(int)
			s.Meta[MetaOverflowRetries] = n + 1
			return next(ctx, s)
		}
	})
}

// overflowTarget picks the forced-compaction MaxTokens: the provider-reported
// limit, else the run's context window, else a fraction of the current
// prompt estimate.
func (comp *component) overflowTarget(s *gantry.State, err error) int {
	var cle *gantry.ContextLengthError
	if errors.As(err, &cle) && cle.Limit > 0 {
		return cle.Limit
	}
	if s.ContextWindow > 0 {
		return s.ContextWindow
	}
	return EstimatePromptTokens(s, comp.b) * overflowFallbackPercent / 100
}
