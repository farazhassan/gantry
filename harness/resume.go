package harness

import (
	"context"
	"errors"
)

// RunFrom starts a new turn seeded from prior, appends input as a new user
// message, and runs the normal phase loop. It carries the conversation
// (Messages), cumulative Usage, and Meta forward; everything else (per-run
// scratch, termination, assembled context, Trace) starts fresh.
//
// prior == nil behaves exactly like Run(ctx, input), so the first turn of a
// session needs no special-casing. The returned *State is always non-nil and
// follows the same termination contract as Run.
func (a *Agent) RunFrom(ctx context.Context, prior *State, input string) (*State, error) {
	if prior == nil {
		return a.Run(ctx, input)
	}
	return a.run(ctx, newStateFrom(prior, input))
}

// Resume continues prior as-is — no new input, no reset — until termination.
// It is the primitive for finishing an interrupted run. A terminal prior
// (Done == true) is returned unchanged (no-op). In every case Resume returns the
// same *State that was passed in — it runs in place rather than copying — so the
// result aliases prior; treat them as one object, and check prior.Done before
// the call if you need to know whether a turn actually ran. prior must be
// non-nil; a nil prior returns a fresh empty State and an error, honoring the
// Run-family contract that the returned *State is never nil.
//
// Note: the in-repo checkpointer saves only at PhaseEnd (terminal state), so
// resuming a loaded checkpoint is typically a no-op. True mid-run crash recovery
// requires a mid-run checkpointer, which is out of scope.
func (a *Agent) Resume(ctx context.Context, prior *State) (*State, error) {
	if prior == nil {
		return NewState(""), errors.New("gantry: Resume requires a non-nil prior state")
	}
	if prior.Done {
		return prior, nil
	}
	return a.run(ctx, prior)
}

// newStateFrom builds the next turn's State from prior. Messages are copied into
// an independent slice and the new user message is appended; because
// DefaultStartHandler no-ops on a non-empty transcript, this is the single
// source of the new user message (no double-seed). Usage is carried so the
// session accumulates cumulative tokens/cost. Meta is shallow-copied into a new
// map. All per-run scratch is left zero-valued.
func newStateFrom(prior *State, input string) *State {
	msgs := make([]Message, len(prior.Messages))
	copy(msgs, prior.Messages)
	msgs = append(msgs, Message{Role: RoleUser, Content: input})

	meta := make(map[string]any, len(prior.Meta))
	for k, v := range prior.Meta {
		meta[k] = v
	}

	return &State{
		Input:    input,
		Messages: msgs,
		Usage:    prior.Usage,
		Meta:     meta,
		Trace:    NewTrace(),
	}
}
