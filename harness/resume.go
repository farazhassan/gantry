package harness

import "context"

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
