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
// See docs/superpowers/specs/2026-06-11-gantry-base-library-design.md
// for the full design.
package harness
