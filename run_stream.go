package gantry

import (
	"context"
	"errors"
)

// RunStream executes the agent loop like Run, additionally emitting whole-run
// Events (phase transitions, token deltas, tool calls/results, and a terminal
// done event) to sink. The sink is called synchronously on the run goroutine;
// returning an error from it aborts the run and propagates the error.
//
// Cancellation follows ctx: a web server should pass the request context so a
// client disconnect stops the run. Like Run, the returned *State is always
// non-nil, even on error.
//
// Event pairing is not guaranteed on error: if a phase handler (or the sink
// itself) returns an error, the run aborts immediately, so the in-flight
// phase emits its phase_start but no phase_end, and no terminal done event
// follows. Consumers should treat a non-nil error from RunStream — not the
// absence of a phase_end — as the signal that the stream ended early.
//
// sink must be non-nil; use Run for the non-streaming case.
func (a *Agent) RunStream(ctx context.Context, input string, sink EventSink) (*State, error) {
	if sink == nil {
		return NewState(input), errors.New("gantry: RunStream requires a non-nil EventSink")
	}
	return a.run(ctx, NewState(input), sink)
}

// RunFromStream is the streaming counterpart of RunFrom: it seeds a new turn
// from prior (carrying Messages, Usage, and Meta forward), appends input, and
// runs the loop while emitting whole-run Events to sink. See RunStream for the
// event-pairing and cancellation contract.
//
// prior == nil behaves like RunStream(ctx, input, sink), so a session's first
// turn needs no special-casing. sink must be non-nil; use RunFrom for the
// non-streaming case. The returned *State is always non-nil, even on error.
func (a *Agent) RunFromStream(ctx context.Context, prior *State, input string, sink EventSink) (*State, error) {
	if sink == nil {
		return NewState(input), errors.New("gantry: RunFromStream requires a non-nil EventSink")
	}
	if prior == nil {
		return a.run(ctx, NewState(input), sink)
	}
	return a.run(ctx, newStateFrom(prior, input), sink)
}

// ResumeStream is the streaming counterpart of Resume: it continues a
// non-terminal prior in place until termination, emitting whole-run Events to
// sink (see RunStream for the event-pairing and cancellation contract). As with
// Resume, a terminal prior (Done == true) is returned unchanged with no events,
// and the result aliases prior.
//
// sink must be non-nil; use Resume for the non-streaming case. prior must be
// non-nil; a nil prior returns a fresh empty State and an error, honoring the
// Run-family contract that the returned *State is never nil.
func (a *Agent) ResumeStream(ctx context.Context, prior *State, sink EventSink) (*State, error) {
	if sink == nil {
		return NewState(""), errors.New("gantry: ResumeStream requires a non-nil EventSink")
	}
	if prior == nil {
		return NewState(""), errors.New("gantry: ResumeStream requires a non-nil prior state")
	}
	if prior.Done {
		return prior, nil
	}
	return a.run(ctx, prior, sink)
}

// emitPhaseEffects emits the State-derived events produced by a phase: tool
// calls become visible after PhasePostLLM (state.PendingToolCalls), and tool
// results after PhaseToolExec (state.ToolResults, before PhaseObserve clears
// them). It is a no-op when no sink is active.
func (a *Agent) emitPhaseEffects(ctx context.Context, ph Phase, state *State) error {
	if sinkFrom(ctx) == nil {
		return nil
	}
	switch ph {
	case PhasePostLLM:
		for i := range state.PendingToolCalls {
			tc := state.PendingToolCalls[i]
			if err := emit(ctx, Event{Type: EventToolCall, Iteration: state.Iteration, ToolCall: &tc}); err != nil {
				return err
			}
		}
	case PhaseToolExec:
		for i := range state.ToolResults {
			tr := state.ToolResults[i]
			if err := emit(ctx, Event{Type: EventToolResult, Iteration: state.Iteration, ToolResult: &tr}); err != nil {
				return err
			}
		}
	}
	return nil
}
