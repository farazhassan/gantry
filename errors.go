package gantry

import "errors"

// Sentinel errors returned by middleware and inspected by the loop and by
// downstream code via errors.Is / errors.As.
var (
	ErrLLMTransient     = errors.New("gantry: LLM transient error")
	ErrLLMPermanent     = errors.New("gantry: LLM permanent error")
	ErrToolExecution    = errors.New("gantry: tool execution error")
	ErrGuardrailBlocked = errors.New("gantry: guardrail blocked")
	ErrLimitExceeded    = errors.New("gantry: limit exceeded")
	ErrHumanAborted     = errors.New("gantry: human-in-loop aborted")
	ErrCheckpointFailed = errors.New("gantry: checkpoint failed")
	// ErrToolAuth signals that a tool call failed because it requires
	// authentication/authorization that is not currently available. Tools
	// wrap their own errors with this sentinel (e.g. fmt.Errorf("%w: %v",
	// ErrToolAuth, err)) to mark the failure as persistent rather than a
	// one-off: retrying or continuing is expected to fail identically on
	// every subsequent call to that tool.
	ErrToolAuth = errors.New("gantry: tool authentication required")
	// ErrToolPersistent is the general form of ErrToolAuth for any other
	// systemic (non-auth) failure a tool wants to mark as persistent.
	ErrToolPersistent = errors.New("gantry: tool persistent failure")
	// ErrToolPolicyAborted wraps the triggering tool error when a
	// components/tool dispatch batch resolves with FailureDisposition ==
	// HarnessStop (see components/tool.Policy). state.DoneReason is set to
	// DoneToolPolicyAborted alongside this error.
	ErrToolPolicyAborted = errors.New("gantry: tool call policy aborted run")
	// ErrToolSkipped is recorded on any tool call that never ran because an
	// earlier failure in the same dispatch batch set BatchFailureMode to a
	// stopping mode (see components/tool.Policy).
	ErrToolSkipped = errors.New("gantry: tool call skipped due to policy")
)

// DoneReason describes why the agent loop terminated.
type DoneReason string

const (
	DoneNoToolCalls      DoneReason = "no_tool_calls"
	DoneMaxIterations    DoneReason = "max_iterations"
	DoneBudgetExceeded   DoneReason = "budget_exceeded"
	DoneGuardrailBlocked DoneReason = "guardrail_blocked"
	DoneHumanAborted     DoneReason = "human_aborted"
	DoneError            DoneReason = "error"
	// DoneClientToolCall means the run suspended awaiting client fulfillment of
	// a client-side tool call: the model invoked a tool that has no server
	// implementation, so the unfulfilled call(s) are left in
	// state.PendingToolCalls for the caller to fulfill. The suspended State is
	// terminal (Done == true), so Resume/ResumeStream no-op on it as-is: the
	// caller appends a tool-result Message for each pending call and must first
	// clear the terminal fields (Done = false, DoneReason = "", PendingToolCalls
	// = nil) or rebuild a fresh non-terminal State from the transcript before
	// resuming (see tool.Client). Distinct from DoneMaxIterations and
	// the normal DoneNoToolCalls finish.
	DoneClientToolCall DoneReason = "client_tool_call"
	// DoneHandoff means routing middleware terminated the run to hand the
	// conversation to another agent; state.Handoff carries the target and
	// mode (set together — see State.Handoff). The layer above the run acts
	// on it: session.Session resolves and re-runs transfer handoffs when a
	// resolver is configured (session.WithResolver), while task-driven runs
	// treat it as an explicit failure (see task/driver.go).
	DoneHandoff DoneReason = "handoff"
	// DoneToolPolicyAborted means a components/tool dispatch batch hit a
	// failure whose effective FailureDisposition was HarnessStop (either
	// configured directly, or forced by a persistent-error sentinel like
	// ErrToolAuth) — see components/tool.Policy. The returned error wraps
	// ErrToolPolicyAborted.
	DoneToolPolicyAborted DoneReason = "tool_policy_aborted"
)

// TraceCarrier is implemented by errors that carry the partial trace of
// a failed run. Use errors.As to extract it.
type TraceCarrier interface {
	Trace() *Trace
}

// runError wraps an error with the trace captured up to the point of failure.
type runError struct {
	err   error
	trace *Trace
}

func (e *runError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *runError) Unwrap() error { return e.err }

func (e *runError) Trace() *Trace { return e.trace }

// wrapError attaches a trace to err so eval and downstream consumers can
// recover it via errors.As(&TraceCarrier{}). Returns nil if err is nil.
func wrapError(err error, trace *Trace) error {
	if err == nil {
		return nil
	}
	return &runError{err: err, trace: trace}
}
