package gantry

import "encoding/json"

// ToolCall is a request from the LLM to invoke a tool.
// ID uniquely identifies this call within a response so the tool result
// can be linked back via ToolResult.CallID.
type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ToolResult is the outcome of executing a single ToolCall.
// IsError set to true tells downstream middleware (and ultimately the LLM)
// that the tool failed. The Content field carries either the success payload
// or a human/LLM-readable error description.
type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
	Err     error  `json:"-"` // optional; preserved for middleware introspection, not serialized
}

// ToolDef describes a tool to the LLM. Schema is a JSON Schema document
// describing the Input shape.
//
// EarlyStart opts the tool into an EventToolCallStart (see stream.go) the
// moment a streaming adapter learns a call's id and name, before the model
// has finished writing its arguments -- so a UI can show the call while a
// long input is still being generated. It is never sent to the provider.
// Set it only on a tool no PostLLM component drops or rewrites (as
// components/planner does to update_plan): an announced call that never
// reaches EventToolCall leaves the consumer holding a start with no end.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
	EarlyStart  bool
}
