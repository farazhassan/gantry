package task

import "github.com/farazhassan/gantry"

// CriticAuthor is the reserved Message.Name marking critic-authored feedback.
// Such messages are model-facing (kept in Task.Working and sent to the LLM as
// plain system messages) but hidden from user-facing transcript rendering via
// VisibleTranscript. Name never goes on the wire, so it is a safe internal tag.
const CriticAuthor = "critic"

// VisibleTranscript returns msgs with critic-authored feedback removed, for
// rendering conversation history to a user. It drops messages whose Role is
// RoleSystem and whose Name is CriticAuthor. The input slice is not mutated;
// the model-facing transcript (Task.Working) is unaffected.
func VisibleTranscript(msgs []gantry.Message) []gantry.Message {
	out := make([]gantry.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == gantry.RoleSystem && m.Name == CriticAuthor {
			continue
		}
		out = append(out, m)
	}
	return out
}
