package gantry

import "encoding/json"

// PendingResult is returned (as an error, via errors.As) by a
// components/tool.Tool's Invoke, or a components/tool.ResumableTool's
// Resume, that has not finished its call. Middleware that recognizes this
// type suspends the run instead of treating the call as a normal success or
// failure — see components/tool's unified suspend handling
// (SuspendClientCalls) and components/tool.Resume.
type PendingResult struct {
	// Pending lists the still-open calls to surface in the suspended run's
	// PendingToolCalls for a caller to answer — real Name/Input, exactly as
	// the underlying call declared them.
	Pending []ToolCall
	// Resume is opaque continuation state. It is round-tripped through
	// State.Meta (including across a checkpoint save/load) and handed back
	// verbatim to ResumableTool.Resume once every entry in Pending has an
	// answer.
	Resume json.RawMessage
}

// Error implements error. PendingResult is a control-flow signal, not a
// user-facing failure; callers should prefer errors.As over inspecting this
// text.
func (e *PendingResult) Error() string { return "gantry: tool call pending" }
