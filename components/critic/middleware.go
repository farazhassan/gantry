package critic

import (
	"context"

	"github.com/farazhassan/gantry"
)

type component struct{ c Critic }

// New returns a Component that installs PhasePostLLM middleware running the Critic
// on state.LastResponse after DefaultPostLLMHandler. See package doc for the
// accept / reject / rewrite semantics.
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
// "Critic feedback: …" hint. Consequently, if memory.New is registered to run
// after the critic, the rejected assistant message may be persisted. This is
// accepted behavior, not a bug.
func New(c Critic) gantry.Component { return &component{c: c} }

func (comp *component) Install(a *gantry.Agent) error {
	const name = "components/critic:critique"
	return a.UseNamed(gantry.PhasePostLLM, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			if s.LastResponse == nil {
				return nil
			}
			v, err := comp.c.Critique(ctx, s)
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
