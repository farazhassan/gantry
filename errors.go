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
	// state.PendingToolCalls for the caller to fulfill (append a tool result
	// and Resume). Distinct from DoneMaxIterations and the normal
	// DoneNoToolCalls finish.
	DoneClientToolCall DoneReason = "client_tool_call"
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
