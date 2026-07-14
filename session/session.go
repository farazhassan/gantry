package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
)

// Session is a keyed handle to one durable conversation. It is safe for
// concurrent use: turns for a given id are serialized by its mutex.
type Session struct {
	id       string
	agent    *gantry.Agent
	store    checkpointer.Checkpointer
	resolver Resolver // nil ⇒ no transfer-handoff routing
	mu       sync.Mutex
}

// ID returns the session's id.
func (s *Session) ID() string { return s.id }

// Run executes one turn: Load(id) -> agent.RunFrom(prior, input) -> Save(id),
// serialized per session by the mutex.
//
//   - A not-found load is treated as the first turn (prior = nil). Any other
//     load error is returned before running, with a nil State.
//   - A RunFrom error is returned with the non-nil partial State, unsaved.
//   - A save failure returns the terminal State plus ErrSaveFailed (wrapped):
//     the turn completed but was not persisted, so the caller can retry or alert
//     while still having the answer.
func (s *Session) Run(ctx context.Context, input string) (*gantry.State, error) {
	return s.run(ctx, input, nil)
}

// RunStream is the streaming counterpart of Run: it executes one turn with the
// same Load -> RunFrom -> Save contract, additionally emitting whole-run Events
// to sink (see gantry.RunStream). sink must be non-nil; use Run otherwise.
func (s *Session) RunStream(ctx context.Context, input string, sink gantry.EventSink) (*gantry.State, error) {
	return s.run(ctx, input, sink)
}

// maxConsecutiveHandoffs bounds how many transfer handoffs one turn may chain
// before run fails with ErrHandoffLoop. It stops two agents that keep handing
// the conversation to each other from ping-ponging forever.
const maxConsecutiveHandoffs = 3

// run performs one turn under the session mutex. A nil sink runs without
// streaming (Run); a non-nil sink streams Events (RunStream). The Load/Save
// contract and error handling are identical for both and live only here.
//
// Transfer-handoff routing: when the run terminates with DoneReason ==
// gantry.DoneHandoff, Mode == transfer, and a resolver is configured, the
// SAME turn re-runs on the target agent from the accumulated transcript:
// gantry.HandoffState rebuilds a non-terminal State (Messages, Usage, and
// public Meta carry; termination and component-private Meta cleared) and
// Resume/ResumeStream continues it. RunFrom is deliberately NOT used for the
// hop — it appends its input as a new user message, which would duplicate the
// turn's input. Hops are bounded by maxConsecutiveHandoffs; on any routing
// error (loop exceeded, unknown target, hop run error) the turn is NOT saved,
// so the checkpoint stays at the previous turn. With no resolver, or for a
// delegate-mode handoff, the DoneHandoff state falls through to Save and is
// returned as-is.
func (s *Session) run(ctx context.Context, input string, sink gantry.EventSink) (*gantry.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prior, err := s.store.Load(ctx, s.id)
	if err != nil {
		if !errors.Is(err, checkpointer.ErrNotFound) {
			return nil, fmt.Errorf("gantry/session: load %q: %w", s.id, err)
		}
		prior = nil // first turn
	}

	var state *gantry.State
	if sink != nil {
		state, err = s.agent.RunFromStream(ctx, prior, input, sink)
	} else {
		state, err = s.agent.RunFrom(ctx, prior, input)
	}
	if err != nil {
		return state, err
	}

	for hops := 0; s.resolver != nil && isTransferHandoff(state); hops++ {
		if hops >= maxConsecutiveHandoffs {
			return state, fmt.Errorf("%w: session %q chained more than %d handoffs in one turn",
				ErrHandoffLoop, s.id, maxConsecutiveHandoffs)
		}
		target := s.resolver(s.id, state.Handoff)
		if target == nil {
			return state, fmt.Errorf("%w: session %q: no agent for target %q",
				ErrHandoffTargetUnknown, s.id, state.Handoff.Target)
		}
		next := gantry.HandoffState(state)
		if sink != nil {
			state, err = target.ResumeStream(ctx, next, sink)
		} else {
			state, err = target.Resume(ctx, next)
		}
		if err != nil {
			return state, err
		}
	}

	if err := s.store.Save(ctx, s.id, state); err != nil {
		return state, fmt.Errorf("%w: save %q: %w", ErrSaveFailed, s.id, err)
	}
	return state, nil
}

// isTransferHandoff keys on DoneReason, not merely a non-nil Handoff pointer:
// middleware such as the critic clears termination and re-runs the loop on
// reject, which can leave a stale state.Handoff on a run that later ends
// another way. Only a run that actually ENDED with DoneHandoff routes.
func isTransferHandoff(st *gantry.State) bool {
	return st.DoneReason == gantry.DoneHandoff && st.Handoff != nil && st.Handoff.Mode == gantry.HandoffTransfer
}

// History returns the persisted transcript for this session, or an empty slice
// if the session does not exist yet.
func (s *Session) History(ctx context.Context) ([]gantry.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prior, err := s.store.Load(ctx, s.id)
	if err != nil {
		if errors.Is(err, checkpointer.ErrNotFound) {
			return []gantry.Message{}, nil
		}
		return nil, fmt.Errorf("gantry/session: load %q: %w", s.id, err)
	}
	msgs := make([]gantry.Message, len(prior.Messages))
	copy(msgs, prior.Messages)
	return msgs, nil
}
