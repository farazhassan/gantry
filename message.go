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
// Message has two unexported fields. wrapUp identifies the prompt injected by
// the max-iterations wrap-up turn (see IsWrapUpPrompt); tag is a private
// marker set with WithTag. Both survive code that copies Message values, but
// are dropped by code that rebuilds messages field-by-field and by JSON, so
// clients and stored transcripts can never set them.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string // optional speaker name

	wrapUp bool   // set only on the prompt injected by the max-iterations wrap-up turn
	tag    string // private marker; see WithTag
}

// WithTag returns a copy of m carrying tag, a private marker components use to
// recognise messages they created (e.g. components/compactor's summaries).
// Unlike Name, a tag cannot come from untrusted input: JSON never carries it
// and adapters never send it. It is lost when a message is rebuilt
// field-by-field or round-tripped through JSON (e.g. checkpoint resume), after
// which the message is an ordinary one.
func WithTag(m Message, tag string) Message {
	m.tag = tag
	return m
}

// Tag returns the private marker set with WithTag, or "".
func Tag(m Message) string { return m.tag }

// IsWrapUpPrompt reports whether m is the prompt injected by the current
// max-iterations wrap-up turn (MaxIterationsWrapUpPrompt). It is meant for
// middleware that rewrites the transcript (compaction, trimming, redaction),
// which can use it to hold the prompt aside and re-append it unchanged after
// rewriting the rest, so the run can still find and remove it. A message with
// the same content that is not the injected one reports false. Prefer
// WrapUpPromptIndex, which also finds the prompt after another middleware
// rebuilt messages and dropped the marker.
func IsWrapUpPrompt(m Message) bool { return m.wrapUp }
