package compactor

import (
	"bytes"
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
// else 75% of EstimatePromptTokens), then retries the call exactly once. If
// compaction does not change the transcript (same messages, compared by
// content, not just length), the original error is returned without a retry.
//
// The retry wraps only the PhaseLLMCall middleware installed before the
// compactor (middleware composes innermost-first): middleware installed after
// it, such as the limiter or a guardrail, runs once around the whole
// attempt-and-retry, and a user retry middleware installed before the
// compactor sees overflows first. The built-in SlidingWindow and HeadTail
// ignore Budget, so the retry only helps when the strategy shrinks further
// under Force (Summarizing, or a custom compactor).
//
// The max-iterations wrap-up prompt (gantry.IsWrapUpPrompt) is never passed to
// the Compactor: when it is the last message, the Compactor sees the transcript
// without it and the prompt is re-appended unchanged afterwards.
func New(c Compactor, b Budget) gantry.Component { return &component{c: c, b: b} }

func (comp *component) Install(a *gantry.Agent) error {
	if err := a.UseNamed(gantry.PhaseAssembleContext, "components/compactor:compact", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let inner context-assembly middleware populate s.Messages first,
			// then compact the result.
			if err := next(ctx, s); err != nil {
				return err
			}
			before := s.Messages // Compact must not alias its input
			compacted, err := comp.compact(ctx, s.Messages, comp.b)
			if err != nil {
				return err
			}
			s.Messages = compacted
			if !sameMessages(before, compacted) {
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
			compacted, cerr := comp.compact(ctx, s.Messages, b)
			if cerr != nil {
				return errors.Join(err, cerr)
			}
			if sameMessages(s.Messages, compacted) {
				// Nothing changed, so resending would fail identically.
				return err
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

// compact runs the Compactor over msgs. On the max-iterations wrap-up turn the
// last message is the injected wrap-up prompt (gantry.IsWrapUpPrompt); it is
// held out so the Compactor never sees, rewrites, or drops it, and re-appended
// unchanged to the result. Change detection compares like with like, because
// the held-out prompt is identical on both sides.
func (comp *component) compact(ctx context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	n := len(msgs)
	if n == 0 || !gantry.IsWrapUpPrompt(msgs[n-1]) {
		return comp.c.Compact(ctx, msgs, b)
	}
	prompt := msgs[n-1]
	compacted, err := comp.c.Compact(ctx, msgs[:n-1:n-1], b)
	if err != nil {
		return nil, err
	}
	out := make([]gantry.Message, 0, len(compacted)+1)
	out = append(out, compacted...)
	return append(out, prompt), nil
}

// overflowTarget picks the forced-compaction MaxTokens (a budget for Messages
// only): the provider-reported limit, else the run's context window, minus the
// estimated System and Tools tokens and floored at 1; else a fraction of the
// current prompt estimate.
func (comp *component) overflowTarget(s *gantry.State, err error) int {
	var cle *gantry.ContextLengthError
	limit := 0
	if errors.As(err, &cle) && cle.Limit > 0 {
		limit = cle.Limit
	} else if s.ContextWindow > 0 {
		limit = s.ContextWindow
	}
	if limit > 0 {
		return max(limit-estimateFixedTokens(s), 1)
	}
	return EstimatePromptTokens(s, comp.b) * overflowFallbackPercent / 100
}

// sameMessages reports whether a and b are the same transcript: equal length
// and, per message, equal Role, Content, ToolCallID, Name and ToolCalls. Length
// alone misses same-length rewrites such as a summary replacing one message.
func sameMessages(a, b []gantry.Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Role != y.Role || x.Content != y.Content || x.ToolCallID != y.ToolCallID ||
			x.Name != y.Name || len(x.ToolCalls) != len(y.ToolCalls) {
			return false
		}
		for j := range x.ToolCalls {
			p, q := x.ToolCalls[j], y.ToolCalls[j]
			if p.ID != q.ID || p.Name != q.Name || !bytes.Equal(p.Input, q.Input) {
				return false
			}
		}
	}
	return true
}
