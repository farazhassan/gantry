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
// ToolCall.ID it is responding to.
//
// Message has one unexported marker field (wrapUp) that identifies the prompt
// injected by the max-iterations wrap-up turn. It survives Compactors that
// copy Message values, but a custom Compactor that rebuilds messages
// field-by-field drops it and would leave the prompt in the transcript.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string // optional speaker name

	wrapUp bool // set only on the prompt injected by the max-iterations wrap-up turn
}
