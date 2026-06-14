// Package harness provides a phase-based agent loop with onion-style
// middleware for building AI agents in Go.
//
// The agent runs a sequence of named phases (PhaseStart, PhaseAssembleContext,
// PhaseLLMCall, PhasePostLLM, PhaseToolExec, PhaseObserve, PhaseEnd). Each
// phase has its own middleware chain composed at run time. Built-in inner
// handlers handle LLM invocation, tool-call parsing, and observation folding;
// all other behavior is contributed by middleware.
//
// Termination convention: Run always returns a non-nil *State. Resource or
// normal stops (no_tool_calls, max_iterations, budget_exceeded) return a nil
// error; active blocks or aborts (guardrail_blocked, human_aborted) return a
// sentinel error (ErrGuardrailBlocked, ErrHumanAborted) in addition to setting
// state.DoneReason. Inspect state.DoneReason for the reason; use errors.Is for
// the blocking sentinels.
//
// The user must supply an LLMClient implementation. No vendor SDK is imported
// by this package.
//
// For live output, an LLMClient may additionally implement the optional
// StreamingLLMClient interface; Agent.RunStream then emits whole-run Events
// (phase transitions, token deltas, tool calls/results, and a terminal done
// event) to an EventSink callback, suitable for forwarding over SSE or
// WebSocket. Plain Run is unaffected and ignores streaming entirely.
//
// See docs/superpowers/specs/2026-06-11-gantry-base-library-design.md
// for the full design.
package harness
