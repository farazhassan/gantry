// Package tool defines the Tool interface and helpers for registering tools
// with an Agent. WithRegistry wires a caller-owned Registry into an agent;
// WithTools and WithTool are sugar over it. The installed middleware advertises
// tool definitions during PhaseStart and dispatches matching ToolCalls during
// PhaseToolExec. Tool lifetime follows the caller's Registry (or the agent when
// the Registry is not retained elsewhere) — there is no process-global state.
package tool

import (
	"context"
	"encoding/json"

	"github.com/farazhassan/gantry/harness"
)

// Tool is a capability the LLM can invoke. Definition is sent to the LLM
// as part of the tool list; Invoke is called when the LLM emits a matching
// ToolCall.
type Tool interface {
	Definition() harness.ToolDef
	Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
}
