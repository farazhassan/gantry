package critic

import (
	"context"

	"github.com/farazhassan/gantry"
)

// WithCritic installs PhasePostLLM middleware that runs the Critic on
// state.LastResponse:
//
//   - Verdict.ModifyOutput != ""   → rewrites the assistant content
//     (both in state.LastResponse and in the just-appended Messages entry)
//     and FinalOutput if Done.
//
//   - Verdict.Accept == false     → clears PendingToolCalls and
//     LastResponse, appends Verdict.Reason as a user-role hint to
//     state.Messages, and unsets Done so the loop runs again.
//
//   - Verdict.Accept == true      → no further action.
//
// The Critic runs after DefaultPostLLMHandler so the assistant message is
// already appended; we mutate it in-place.
//
// Reject path: when Verdict.Accept == false, the rejected assistant message is
// deliberately LEFT in state.Messages (the live transcript) so the next
// iteration's LLM call sees its rejected attempt alongside the appended
// "Critic feedback: …" hint. Consequently, if WithMemory is registered to run
// after the critic, the rejected assistant message may be persisted. This is
// accepted behavior, not a bug.
func WithCritic(a *gantry.Agent, c Critic) {
	const name = "components/critic:critique"
	_ = a.UseNamed(gantry.PhasePostLLM, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Let downstream (DefaultPostLLMHandler) run first so messages are
			// populated and Done state reflects no-tool-calls.
			if err := next(ctx, s); err != nil {
				return err
			}
			if s.LastResponse == nil {
				return nil
			}
			v, err := c.Critique(ctx, s)
			if err != nil {
				return err
			}
			if v.ModifyOutput != "" {
				s.LastResponse.Content = v.ModifyOutput
				if last := lastAssistantIndex(s.Messages); last >= 0 {
					s.Messages[last].Content = v.ModifyOutput
				}
				if s.Done {
					s.FinalOutput = v.ModifyOutput
				}
			}
			if !v.Accept {
				s.PendingToolCalls = nil
				s.LastResponse = nil
				s.Done = false
				s.DoneReason = ""
				s.FinalOutput = ""
				if v.Reason != "" {
					s.Messages = append(s.Messages, gantry.Message{
						Role:    gantry.RoleUser,
						Content: "Critic feedback: " + v.Reason,
					})
				}
			}
			return nil
		}
	})
}

func lastAssistantIndex(msgs []gantry.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == gantry.RoleAssistant {
			return i
		}
	}
	return -1
}
