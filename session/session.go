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
	id    string
	agent *gantry.Agent
	store checkpointer.Checkpointer
	mu    sync.Mutex
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

// run performs one turn under the session mutex. A nil sink runs without
// streaming (Run); a non-nil sink streams Events (RunStream). The Load/Save
// contract and error handling are identical for both and live only here.
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

	if err := s.store.Save(ctx, s.id, state); err != nil {
		return state, fmt.Errorf("%w: save %q: %w", ErrSaveFailed, s.id, err)
	}
	return state, nil
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
