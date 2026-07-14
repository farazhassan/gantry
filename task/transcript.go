package task

import "github.com/farazhassan/gantry"

// CriticAuthor is the reserved Message.Name marking critic-authored feedback.
// Such messages are model-facing — kept in Task.Working and sent to the LLM as
// user turns, because providers have no mid-transcript system slot (the
// Anthropic adapter, for one, silently folds unknown roles into user turns) —
// but hidden from user-facing transcript rendering via VisibleTranscript. Name
// never goes on the wire, so it is a safe internal tag.
const CriticAuthor = "critic"

// VisibleTranscript returns msgs with critic-authored feedback removed, for
// rendering conversation history to a user. It drops every message whose Name
// is CriticAuthor regardless of Role, so both the current RoleUser feedback
// form and any legacy RoleSystem feedback persisted by earlier versions stay
// hidden. The input slice is not mutated; the model-facing transcript
// (Task.Working) is unaffected.
func VisibleTranscript(msgs []gantry.Message) []gantry.Message {
	out := make([]gantry.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Name == CriticAuthor {
			continue
		}
		out = append(out, m)
	}
	return out
}
