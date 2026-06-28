package memory

import (
	"context"

	"github.com/farazhassan/gantry"
)

type component struct{ m Memory }

// New returns a Component that wires a Memory implementation into the agent.
// It installs a PhaseAssembleContext "components/memory:read" middleware that
// prepends stored history on iteration 0, and a PhasePostLLM
// "components/memory:persist" middleware that appends the user input (iteration 0)
// and the assistant response to the store. Register memory LAST among PhasePostLLM
// components so persist captures the finalized assistant message (see package doc).
//
// Middleware ordering: register memory LAST among the PhasePostLLM components
// (after WithCritic and WithLimiter). PhasePostLLM components that act after
// next() run that work in forward registration order (last-registered = outermost
// = runs last), so registering memory last makes memory:persist capture the
// assistant message after the critic has finalized it (Verdict.ModifyOutput) or
// left the rejected message in the transcript (Verdict.Accept == false).
func New(m Memory) gantry.Component { return &component{m: m} }

func (c *component) Install(a *gantry.Agent) error {
	const readName = "components/memory:read"
	const persistName = "components/memory:persist"

	if err := a.UseNamed(gantry.PhaseAssembleContext, readName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Only read+prepend on the first iteration. The in-run transcript
			// accumulates in s.Messages across iterations, so re-prepending the
			// stored history on later iterations would duplicate prior turns.
			if s.Iteration == 0 {
				hist, err := c.m.Read(ctx)
				if err != nil {
					return err
				}
				// Prepend history to the user input seeded by DefaultStartHandler.
				s.Messages = append(hist, s.Messages...)
			}
			return next(ctx, s)
		}
	}); err != nil {
		return err
	}

	return a.UseNamed(gantry.PhasePostLLM, persistName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Persist the user input on the first iteration.
			if s.Iteration == 0 && s.Input != "" {
				if err := c.m.Append(ctx, gantry.Message{Role: gantry.RoleUser, Content: s.Input}); err != nil {
					return err
				}
			}
			// Run the inner handler first so DefaultPostLLMHandler appends the
			// assistant message to s.Messages, then persist it (preserving any
			// tool_calls it carries).
			if err := next(ctx, s); err != nil {
				return err
			}
			if last := lastAssistant(s.Messages); last != nil {
				if err := c.m.Append(ctx, *last); err != nil {
					return err
				}
			}
			return nil
		}
	})
}

func lastAssistant(msgs []gantry.Message) *gantry.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == gantry.RoleAssistant {
			m := msgs[i]
			return &m
		}
	}
	return nil
}
