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
// injected by the max-iterations wrap-up turn (see IsWrapUpPrompt). It survives
// code that copies Message values, but is dropped by code that rebuilds
// messages field-by-field; the run then falls back to locating the prompt by
// position and content.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string // optional speaker name

	wrapUp bool // set only on the prompt injected by the max-iterations wrap-up turn
}

// IsWrapUpPrompt reports whether m is the prompt injected by the current
// max-iterations wrap-up turn (MaxIterationsWrapUpPrompt). It is meant for
// middleware that rewrites the transcript (compaction, trimming, redaction),
// which can use it to hold the prompt aside and re-append it unchanged after
// rewriting the rest, so the run can still find and remove it. A message with
// the same content that is not the injected one reports false.
func IsWrapUpPrompt(m Message) bool { return m.wrapUp }
