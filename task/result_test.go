package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestResult(t *testing.T) {
	tests := []struct {
		name    string
		working []gantry.Message
		want    string
	}{
		{
			name: "final assistant message",
			working: []gantry.Message{
				{Role: gantry.RoleUser, Content: "do it"},
				{Role: gantry.RoleAssistant, Content: "here is the answer"},
			},
			want: "here is the answer",
		},
		{
			name: "last of several assistant messages",
			working: []gantry.Message{
				{Role: gantry.RoleUser, Content: "do it"},
				{Role: gantry.RoleAssistant, Content: "draft"},
				{Role: gantry.RoleUser, Content: "revise"},
				{Role: gantry.RoleAssistant, Content: "final"},
			},
			want: "final",
		},
		{
			name:    "no assistant message",
			working: []gantry.Message{{Role: gantry.RoleUser, Content: "do it"}},
			want:    "",
		},
		{
			name:    "empty working",
			working: nil,
			want:    "",
		},
		{
			name: "trailing critic feedback is skipped",
			working: []gantry.Message{
				{Role: gantry.RoleAssistant, Content: "the answer"},
				{Role: gantry.RoleSystem, Name: CriticAuthor, Content: "Completion rejected: needs a CTA"},
			},
			want: "the answer",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Result(&Task{Working: tc.working})
			if got != tc.want {
				t.Errorf("Result() = %q, want %q", got, tc.want)
			}
		})
	}
}
