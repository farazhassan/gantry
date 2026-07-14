package task

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
)

// criticVerifier adapts a critic.Critic into a task.Verifier. It presents the
// task as a State view — transcript pruned of critic-authored feedback, the
// plan (carrying per-step AcceptanceCriteria), and the last assistant message
// as LastResponse — and asks the critic to judge whole-task completion
// against the criteria.
type criticVerifier struct{ c critic.Critic }

// NewCriticVerifier wraps a Critic so it can gate the task driver's done
// transition via WithVerifier.
func NewCriticVerifier(c critic.Critic) Verifier { return criticVerifier{c: c} }

// Verify builds the State view and maps the Verdict to the Verifier contract.
// A task with no non-empty assistant answer is rejected outright ("no final
// answer produced") without consulting the critic — the old behavior handed
// the critic a nil LastResponse, which LLMCritic treats as an auto-pass. The
// transcript view is pruned of prior critic-authored feedback (Name ==
// CriticAuthor) so the critic never re-reads its own verdicts, which would
// snowball its context run after run. A Critique error is treated as a soft
// reject ("cannot confirm done; keep working") rather than propagated, so the
// task neither crashes nor falsely completes.
func (v criticVerifier) Verify(ctx context.Context, t *Task) (bool, string) {
	last := lastAssistant(t.Working)
	if last == nil {
		return false, "no final answer produced"
	}
	state := &gantry.State{
		// VisibleTranscript both prunes critic-authored messages and returns a
		// fresh slice, so a critic cannot mutate the ledger through the view.
		Messages:     VisibleTranscript(t.Working),
		Plan:         t.Plan,
		LastResponse: last,
		Meta:         map[string]any{},
	}
	verdict, err := v.c.Critique(ctx, state)
	if err != nil {
		return false, fmt.Sprintf("critic error: %v", err)
	}
	return verdict.Accept, verdict.Reason
}

// lastAssistant returns the most recent assistant message with non-empty
// Content as an LLMResponse, or nil if none exists.
func lastAssistant(msgs []gantry.Message) *gantry.LLMResponse {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == gantry.RoleAssistant && msgs[i].Content != "" {
			return &gantry.LLMResponse{Content: msgs[i].Content}
		}
	}
	return nil
}
