// Package tool defines the Tool interface and helpers for registering tools
// with an Agent. New wires a caller-owned Registry into an agent;
// FromTools is sugar over it for callers that do not need to retain the Registry.
// Client declares definition-only "client-side" tools that suspend the run when
// called. The installed middleware advertises tool definitions during PhaseStart
// and dispatches matching ToolCalls during PhaseToolExec. Tool lifetime follows
// the caller's Registry (or the agent when the Registry is not retained
// elsewhere) — there is no process-global state.
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
