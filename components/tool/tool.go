// Package tool defines the Tool interface and helpers for registering tools
// with an Agent. New wires a caller-owned Registry into an agent;
// FromTools is sugar over it for callers that do not need to retain the Registry.
// Client declares definition-only "client-side" tools that suspend the run when
// called. The installed middleware advertises tool definitions during PhaseStart
// and dispatches matching ToolCalls during PhaseToolExec. Tool lifetime follows
// the caller's Registry (or the agent when the Registry is not retained
// elsewhere) — there is no process-global state.
//
// DynamicClient is the per-request counterpart to Client: instead of a fixed
// tool list at agent-construction time, a caller marks tool defs as
// client-side for the very next run/resume call via SetPendingClientTools
// (e.g. an AG-UI handler decoding per-request tool declarations from a
// CopilotKit frontend action). Client and DynamicClient are mutually
// exclusive on one agent — each explicitly checks for the other at install
// time, so installing both returns an error regardless of install order. Use
// SuspendClientCallsInstalled or DynamicClientInstalled to check ahead of
// time whether an agent supports the (static or per-request, respectively)
// client-tool mechanism.
package tool

import (
	"context"
	"encoding/json"

	"github.com/farazhassan/gantry"
)

// Tool is a capability the LLM can invoke. Definition is sent to the LLM
// as part of the tool list; Invoke is called when the LLM emits a matching
// ToolCall.
type Tool interface {
	Definition() gantry.ToolDef
	Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}

// ResumableTool is an optional extension of Tool for a call that can outlive
// one Invoke. Implement it alongside Tool when Invoke may return a
// *gantry.PendingResult: once every entry in that PendingResult's Pending
// has an answer, Resume is called to continue (or finish) the call.
type ResumableTool interface {
	Tool
	// Resume continues a call using the token from a prior
	// *gantry.PendingResult.Resume and the answers for everything that
	// PendingResult's Pending listed, matched by ToolResult.CallID against
	// each Pending entry's ID. It may itself return another
	// *gantry.PendingResult (still not done) or a normal
	// (json.RawMessage, nil) / (nil, error) outcome, exactly like Invoke.
	Resume(ctx context.Context, resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error)
}
