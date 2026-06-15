package checkpointer_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// TestStateJSONRoundTrip guards the FileCheckpointer's serialization path:
// harness.State must survive Marshal->Unmarshal for every field the
// checkpointer persists. ToolResult.Err is json:"-" by design and is not
// part of the persisted contract, so it is excluded from the fixture.
func TestStateJSONRoundTrip(t *testing.T) {
	orig := &harness.State{
		Input:  "hello",
		Task:   "demo",
		System: "you are helpful",
		Messages: []harness.Message{
			{Role: harness.RoleUser, Content: "hi"},
			{
				Role:      harness.RoleAssistant,
				Content:   "calling a tool",
				ToolCalls: []harness.ToolCall{{ID: "c1", Name: "fs__read_file", Input: json.RawMessage(`{"path":"/tmp/x"}`)}},
			},
			{Role: harness.RoleTool, Content: "file body", ToolCallID: "c1", Name: "fs__read_file"},
		},
		Tools:       []harness.ToolDef{{Name: "fs__read_file", Description: "reads", Schema: json.RawMessage(`{"type":"object"}`)}},
		Iteration:   2,
		Done:        true,
		DoneReason:  harness.DoneNoToolCalls,
		FinalOutput: "done",
		Usage:       harness.Usage{InputTokens: 10, OutputTokens: 5},
		Meta:        map[string]any{"note": "string-valued meta round-trips"},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got harness.State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(*orig, got) {
		t.Fatalf("round-trip mismatch:\n orig=%#v\n got =%#v", *orig, got)
	}
}
