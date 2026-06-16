package agui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestToRunSplitsHistoryAndLastUser(t *testing.T) {
	in := &RunAgentInput{
		ThreadID: "t1",
		RunID:    "r1",
		Messages: []InputMessage{
			{Role: "system", Content: "be nice"},
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "hi there"},
			{Role: "user", Content: "second"},
		},
	}
	prior, input, err := in.ToRun()
	if err != nil {
		t.Fatalf("ToRun: %v", err)
	}
	if input != "second" {
		t.Fatalf("input = %q, want %q", input, "second")
	}
	want := []harness.Message{
		{Role: harness.RoleSystem, Content: "be nice"},
		{Role: harness.RoleUser, Content: "first"},
		{Role: harness.RoleAssistant, Content: "hi there"},
	}
	if !reflect.DeepEqual(prior.Messages, want) {
		t.Fatalf("prior.Messages =\n%#v\nwant\n%#v", prior.Messages, want)
	}
}

func TestToRunMapsToolLinkage(t *testing.T) {
	in := &RunAgentInput{
		Messages: []InputMessage{
			{Role: "assistant", ToolCalls: []InputToolCall{
				{ID: "c1", Type: "function", Function: InputToolFunction{Name: "search", Arguments: `{"q":"x"}`}},
			}},
			{Role: "tool", ToolCallID: "c1", Content: "result"},
			{Role: "user", Content: "go on"},
		},
	}
	prior, _, err := in.ToRun()
	if err != nil {
		t.Fatalf("ToRun: %v", err)
	}
	assistant := prior.Messages[0]
	if len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].ID != "c1" ||
		assistant.ToolCalls[0].Name != "search" || string(assistant.ToolCalls[0].Input) != `{"q":"x"}` {
		t.Fatalf("assistant tool call not mapped: %#v", assistant.ToolCalls)
	}
	tool := prior.Messages[1]
	if tool.Role != harness.RoleTool || tool.ToolCallID != "c1" || tool.Content != "result" {
		t.Fatalf("tool message not mapped: %#v", tool)
	}
}

func TestToRunErrors(t *testing.T) {
	cases := []struct {
		name string
		in   *RunAgentInput
	}{
		{"empty", &RunAgentInput{Messages: nil}},
		{"last_not_user", &RunAgentInput{Messages: []InputMessage{{Role: "assistant", Content: "hi"}}}},
		{"unknown_role", &RunAgentInput{Messages: []InputMessage{{Role: "wizard", Content: "?"}, {Role: "user", Content: "hi"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := tc.in.ToRun(); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestRunAgentInputDecodes(t *testing.T) {
	raw := `{"threadId":"t1","runId":"r1","messages":[{"role":"user","content":"hi"}]}`
	var in RunAgentInput
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if in.ThreadID != "t1" || len(in.Messages) != 1 || in.Messages[0].Content != "hi" {
		t.Fatalf("decoded unexpectedly: %#v", in)
	}
}
