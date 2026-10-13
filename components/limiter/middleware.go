package limiter

import (
	"context"
	"errors"

	"github.com/farazhassan/gantry"
)

type component struct{ l Limiter }

// New returns a Component that installs token-budget middleware: a PhaseLLMCall
// pre-check (which also records tokens a failed call spent, since PostLLM won't
// run for it), a PhasePostLLM usage recorder, and a PhasePostLLM finalize that
// terminates the loop when the limit is exceeded. Usage added to State.Usage
// outside the agent's own LLM calls — e.g. a compactor's summarizer call, or an
// overflow handler — is recorded at the next pre-check, so it counts toward
// the budget too. Register limiter before
// transcript so transcript:persist observes the finalized turn (see package doc).
//
// When the limit is exceeded mid-run, state.Done is set with
// DoneBudgetExceeded. The current iteration's response is still appended.
//
// Middleware ordering: among the PhasePostLLM steps, record does its work
// before next() (pre-next), while finalize does its work after next()
// (post-next). Pre-next work runs in reverse registration order and post-next
// work in forward order (last-registered = outermost = runs last). Register
// transcript.New after limiter and critic so transcript:persist observes the
// finalized turn. See the transcript package's "Middleware ordering" note.
func New(l Limiter) gantry.Component { return &component{l: l} }

func (c *component) Install(a *gantry.Agent) error {
	const baselineName = "components/limiter:baseline"
	const checkName = "components/limiter:check"
	const recordName = "components/limiter:record"
	const finalizeName = "components/limiter:finalize"

	if err := a.UseNamed(gantry.PhaseStart, baselineName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			markRecorded(s) // usage carried from earlier runs was recorded then
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	if err := a.UseNamed(gantry.PhaseLLMCall, checkName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			c.recordUnrecorded(ctx, s)
			if err := c.l.Check(ctx, s); err != nil {
				if errors.Is(err, gantry.ErrLimitExceeded) {
					s.Done = true
					s.DoneReason = gantry.DoneBudgetExceeded
					return nil
				}
				return err
			}
			err := next(ctx, s)
			if err != nil {
				// A failed call can still have spent tokens (e.g. a
				// context-window stop turned into an overflow error). PostLLM
				// won't run to record them, so record them here, once.
				c.recordUnrecorded(ctx, s)
			}
			return err
		}
	}); err != nil {
		return err
	}

	if err := a.UseNamed(gantry.PhasePostLLM, recordName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if s.LastResponse != nil {
				c.l.Record(ctx, s.LastResponse.Usage)
			}
			markRecorded(s)
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhasePostLLM, finalizeName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if err := c.l.Check(ctx, s); err != nil && errors.Is(err, gantry.ErrLimitExceeded) {
				s.Done = true
				s.DoneReason = gantry.DoneBudgetExceeded
			}
			return nil
		}
	})
}

// recordedUsageKey is the State.Meta key holding the State.Usage the limiter
// has recorded so far this run.
const recordedUsageKey = "components/limiter:recorded_usage"

// markRecorded notes that everything in s.Usage has been recorded.
func markRecorded(s *gantry.State) {
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	s.Meta[recordedUsageKey] = s.Usage
}

// recordUnrecorded records State.Usage growth since the last markRecorded
// (spend outside the agent's own LLM calls, or by a failed call) and marks
// it recorded. Without a baseline from this run (e.g. a state restored from
// JSON), it only sets one, so earlier usage is never counted twice.
func (c *component) recordUnrecorded(ctx context.Context, s *gantry.State) {
	if seen, ok := s.Meta[recordedUsageKey].(gantry.Usage); ok {
		if spent := usageSince(seen, s.Usage); spent != (gantry.Usage{}) {
			c.l.Record(ctx, spent)
		}
	}
	markRecorded(s)
}

// usageSince returns the usage added to state between before and after.
func usageSince(before, after gantry.Usage) gantry.Usage {
	return gantry.Usage{
		InputTokens:      after.InputTokens - before.InputTokens,
		OutputTokens:     after.OutputTokens - before.OutputTokens,
		CacheReadTokens:  after.CacheReadTokens - before.CacheReadTokens,
		CacheWriteTokens: after.CacheWriteTokens - before.CacheWriteTokens,
		Cost:             after.Cost - before.Cost,
	}
}
