package memory

import (
	"context"

	"github.com/farazhassan/gantry"
)

// WithMemory wires a Memory implementation into the agent. It installs:
//
//   - PhaseAssembleContext middleware "components/memory:read" that prepends
//     the stored history to state.Messages (so the user's current Input,
//     seeded by DefaultStartHandler, comes AFTER history). This runs only on
//     iteration 0; PhaseAssembleContext re-runs every iteration and the
//     in-run transcript already accumulates in state.Messages, so re-prepending
//     would duplicate prior turns.
//
//   - PhasePostLLM middleware "components/memory:persist" that appends the
//     user input (on iteration 0) and the assistant response to the store.
//     It runs the inner handler first so the assistant message (with any
//     tool_calls) is present in state.Messages before being persisted.
//
// Middleware ordering: register WithMemory LAST among the PhasePostLLM
// components (after WithCritic and WithLimiter). PhasePostLLM components that
// act after next() run that work in forward registration order (last-registered
// = outermost = runs last), so registering memory last makes memory:persist
// capture the assistant message after the critic has finalized it
// (Verdict.ModifyOutput) or left the rejected message in the transcript
// (Verdict.Accept == false).
func WithMemory(a *gantry.Agent, m Memory) {
	const readName = "components/memory:read"
	const persistName = "components/memory:persist"

	_ = a.UseNamed(gantry.PhaseAssembleContext, readName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Only read+prepend on the first iteration. The in-run transcript
			// accumulates in s.Messages across iterations, so re-prepending the
			// stored history on later iterations would duplicate prior turns.
			if s.Iteration == 0 {
				hist, err := m.Read(ctx)
				if err != nil {
					return err
				}
				// Prepend history to the user input seeded by DefaultStartHandler.
				s.Messages = append(hist, s.Messages...)
			}
			return next(ctx, s)
		}
	})

	_ = a.UseNamed(gantry.PhasePostLLM, persistName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Persist the user input on the first iteration.
			if s.Iteration == 0 && s.Input != "" {
				if err := m.Append(ctx, gantry.Message{Role: gantry.RoleUser, Content: s.Input}); err != nil {
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
				if err := m.Append(ctx, *last); err != nil {
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
