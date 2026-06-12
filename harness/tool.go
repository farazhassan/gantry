package harness

import "encoding/json"

// ToolCall is a request from the LLM to invoke a tool.
// ID uniquely identifies this call within a response so the tool result
// can be linked back via ToolResult.CallID.
type ToolCall struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ToolResult is the outcome of executing a single ToolCall.
// IsError set to true tells downstream middleware (and ultimately the LLM)
// that the tool failed. The Content field carries either the success payload
// or a human/LLM-readable error description.
type ToolResult struct {
	CallID  string
	Content string
	IsError bool
	Err     error // optional; preserved for middleware introspection
}

// ToolDef describes a tool to the LLM. Schema is a JSON Schema document
// describing the Input shape.
type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}
