package harness_test

import (
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestRoleConstants(t *testing.T) {
	cases := []struct {
		role harness.Role
		want string
	}{
		{harness.RoleSystem, "system"},
		{harness.RoleUser, "user"},
		{harness.RoleAssistant, "assistant"},
		{harness.RoleTool, "tool"},
	}
	for _, c := range cases {
		if string(c.role) != c.want {
			t.Errorf("Role %v = %q, want %q", c.role, string(c.role), c.want)
		}
	}
}

func TestMessageZeroValue(t *testing.T) {
	var m harness.Message
	if m.Role != "" {
		t.Errorf("zero Message.Role = %q, want empty", m.Role)
	}
	if m.Content != "" {
		t.Errorf("zero Message.Content = %q, want empty", m.Content)
	}
	if len(m.ToolCalls) != 0 {
		t.Errorf("zero Message.ToolCalls len = %d, want 0", len(m.ToolCalls))
	}
}
