package harness

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
// ToolCall.ID it is responding to.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string // optional speaker name
}
