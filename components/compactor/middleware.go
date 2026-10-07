package compactor

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

// MetaOverflowRetries is the State.Meta key holding how many times the
// overflow retry compacted and re-sent a request this run (an int). It is
// cleared at PhaseStart, so it counts per Run, RunFrom or Resume call even
// though RunFrom carries Meta across turns.
const MetaOverflowRetries = "components/compactor:overflow_retries"

// Middleware names this component registers.
const (
	compactName      = "components/compactor:compact"
	resetRetriesName = "components/compactor:reset_retries"
)

// overflowFallbackPercent is the target, as a percentage of the current
// message tokens, when neither the provider error nor State.ContextWindow
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
// It also registers the agent's gantry.ContextOverflowHandler (so an agent can
// have only one compactor): when the LLM call fails with
// gantry.ErrContextLengthExceeded, it compacts once more with Budget.Force set
// and MaxTokens (a budget for Messages) set to the provider-reported limit,
// else State.ContextWindow, minus the estimated System and Tools tokens; else
// 75% of the current message tokens. The core loop then re-runs the whole
// PhaseLLMCall middleware chain exactly once, so input guardrails, the limiter
// and other LLM-call middleware check the compacted input regardless of
// install order. If compaction does not change the transcript (same messages,
// compared by content, not just length), the original error is returned
// without a retry. The built-in SlidingWindow and HeadTail ignore Budget, so
// the retry only helps when the strategy shrinks further under Force
// (Summarizing, or a custom compactor).
//
// The max-iterations wrap-up prompt (gantry.IsWrapUpPrompt) is never passed to
// the Compactor: wherever it sits, the Compactor sees only the messages before
// it, and the prompt plus anything appended after it are re-appended unchanged.
func New(c Compactor, b Budget) gantry.Component { return &component{c: c, b: b} }

func (comp *component) Install(a *gantry.Agent) error {
	// Install atomically: check both middleware names, then claim the single
	// overflow-handler slot; only then register anything, so a failure
	// leaves the agent untouched.
	for _, mw := range []struct {
		phase gantry.Phase
		name  string
	}{{gantry.PhaseStart, resetRetriesName}, {gantry.PhaseAssembleContext, compactName}} {
		for _, n := range a.MiddlewareNames(mw.phase) {
			if n == mw.name {
				return fmt.Errorf("compactor: middleware %q already registered on phase %q", mw.name, mw.phase)
			}
		}
	}
	if err := a.OnContextOverflow(comp.onOverflow); err != nil {
		return err
	}
	if err := a.UseNamed(gantry.PhaseStart, resetRetriesName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			delete(s.Meta, MetaOverflowRetries) // per-run count; Meta is carried by RunFrom
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}
	if err := a.UseNamed(gantry.PhaseAssembleContext, compactName, func(next gantry.Handler) gantry.Handler {
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
	return nil
}

// onOverflow is the agent's gantry.ContextOverflowHandler: it compacts with
// Budget.Force toward overflowTarget and asks the core loop to re-run the
// whole PhaseLLMCall chain once. It declines (no retry) when compaction
// leaves the transcript unchanged, since resending would fail identically.
func (comp *component) onOverflow(ctx context.Context, s *gantry.State, err error) (bool, error) {
	b := comp.b
	b.Force = true
	b.MaxTokens = comp.overflowTarget(s, err)
	compacted, cerr := comp.compact(ctx, s.Messages, b)
	if cerr != nil {
		return false, cerr
	}
	if sameMessages(s.Messages, compacted) {
		return false, nil
	}
	s.Messages = compacted
	s.ContextUsage = gantry.ContextUsage{}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	n, _ := s.Meta[MetaOverflowRetries].(int)
	s.Meta[MetaOverflowRetries] = n + 1
	return true, nil
}

// compact runs the Compactor over msgs. On the max-iterations wrap-up turn the
// injected wrap-up prompt (gantry.IsWrapUpPrompt) is held out wherever it sits,
// together with anything appended after it, so the Compactor never sees,
// rewrites, or drops it; that suffix is re-appended unchanged to the result,
// and its estimated tokens are taken out of a non-zero Budget.MaxTokens first.
// Change detection compares like with like, because the held-out suffix is
// identical on both sides.
func (comp *component) compact(ctx context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	i := lastWrapUpPrompt(msgs)
	if i < 0 {
		return comp.c.Compact(ctx, msgs, b)
	}
	suffix := msgs[i:]
	if b.MaxTokens > 0 {
		// The held-out suffix is re-sent as-is, so the Compactor's share of
		// the budget is what remains after it.
		for _, m := range suffix {
			b.MaxTokens -= b.Count(m)
		}
		b.MaxTokens = max(b.MaxTokens, 1)
	}
	compacted, err := comp.c.Compact(ctx, msgs[:i:i], b)
	if err != nil {
		return nil, err
	}
	out := make([]gantry.Message, 0, len(compacted)+len(suffix))
	out = append(out, compacted...)
	return append(out, suffix...), nil
}

// lastWrapUpPrompt returns the index of the last injected wrap-up prompt in
// msgs, or -1.
func lastWrapUpPrompt(msgs []gantry.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if gantry.IsWrapUpPrompt(msgs[i]) {
			return i
		}
	}
	return -1
}

// overflowTarget picks the forced-compaction MaxTokens (a budget for Messages
// only): the provider-reported limit, else the run's context window, minus the
// estimated System and Tools tokens; else a fraction of the current message
// tokens (the prompt estimate minus System and Tools). Floored at 1.
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
	messages := EstimatePromptTokens(s, comp.b) - estimateFixedTokens(s)
	return max(messages*overflowFallbackPercent/100, 1)
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
