package gantry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMarshalMessages_RolesAndToolLinkage(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "calc", Input: json.RawMessage(`{"a":1}`)}}},
		{Role: RoleTool, Content: "42", ToolCallID: "c1"},
	}
	out := marshalMessages("you are frank", msgs)

	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if len(got) != 4 { // system prepended
		t.Fatalf("want 4 messages (system + 3), got %d: %s", len(got), out)
	}
	if got[0]["role"] != "system" || got[0]["content"] != "you are frank" {
		t.Fatalf("system message wrong: %v", got[0])
	}
	if !strings.Contains(out, `"tool_calls"`) || !strings.Contains(out, `"tool_call_id":"c1"`) {
		t.Fatalf("tool linkage missing: %s", out)
	}
}

func TestMarshalResponse_AssistantMessage(t *testing.T) {
	out := marshalResponse(LLMResponse{Content: "hello", ToolCalls: []ToolCall{{ID: "x", Name: "t", Input: json.RawMessage(`{}`)}}})
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got["role"] != "assistant" || got["content"] != "hello" {
		t.Fatalf("assistant message wrong: %v", got)
	}
}
