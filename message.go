package gantry

// Role identifies the author of a message.
type Role string

// Standard roles. Adapters may produce any string but should normalize to these.
const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry in a conversation transcript.
//
// ToolCalls is non-empty only on assistant messages that requested tool use.
// ToolCallID is set only on tool-role messages and links back to the
// ToolCall.ID it is responding to. IsError, also tool-role only, marks the
// tool call as failed (carried over from ToolResult.IsError); LLM adapters
// pass it to providers with a native error flag (Anthropic's is_error) and
// otherwise prefix Content with "Error: ".
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	IsError    bool
	Name       string // optional speaker name
}
