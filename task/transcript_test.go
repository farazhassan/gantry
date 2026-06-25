package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestVisibleTranscriptDropsCriticMessages(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleUser, Content: "do it"},
		{Role: gantry.RoleSystem, Name: CriticAuthor, Content: "Completion rejected: nope"},
		{Role: gantry.RoleAssistant, Content: "done"},
	}
	got := VisibleTranscript(msgs)
	if len(got) != 2 {
		t.Fatalf("got %d messages, want 2; %+v", len(got), got)
	}
	for _, m := range got {
		if m.Role == gantry.RoleSystem && m.Name == CriticAuthor {
			t.Errorf("critic message leaked into visible transcript: %+v", m)
		}
	}
}

func TestVisibleTranscriptKeepsPlainSystemAndNamedUsers(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleSystem, Content: "a real directive"},         // plain system: keep
		{Role: gantry.RoleUser, Name: "alice", Content: "hi"},          // named user: keep
		{Role: gantry.RoleSystem, Name: CriticAuthor, Content: "hide"}, // drop
	}
	got := VisibleTranscript(msgs)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2; %+v", len(got), got)
	}
}

func TestVisibleTranscriptDoesNotMutateInput(t *testing.T) {
	msgs := []gantry.Message{
		{Role: gantry.RoleSystem, Name: CriticAuthor, Content: "hide"},
		{Role: gantry.RoleUser, Content: "keep"},
	}
	_ = VisibleTranscript(msgs)
	if len(msgs) != 2 {
		t.Errorf("input mutated: len = %d, want 2", len(msgs))
	}
}
