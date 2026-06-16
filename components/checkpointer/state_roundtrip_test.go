package checkpointer_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

// TestStateJSONRoundTrip guards the FileCheckpointer's serialization path:
// gantry.State must survive Marshal->Unmarshal for every field the
// checkpointer persists. ToolResult.Err is json:"-" by design and is not
// part of the persisted contract, so it is excluded from the fixture.
func TestStateJSONRoundTrip(t *testing.T) {
	orig := &gantry.State{
		Input:  "hello",
		Task:   "demo",
		System: "you are helpful",
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "hi"},
			{
				Role:      gantry.RoleAssistant,
				Content:   "calling a tool",
				ToolCalls: []gantry.ToolCall{{ID: "c1", Name: "fs__read_file", Input: json.RawMessage(`{"path":"/tmp/x"}`)}},
			},
			{Role: gantry.RoleTool, Content: "file body", ToolCallID: "c1", Name: "fs__read_file"},
		},
		Tools:       []gantry.ToolDef{{Name: "fs__read_file", Description: "reads", Schema: json.RawMessage(`{"type":"object"}`)}},
		Iteration:   2,
		Done:        true,
		DoneReason:  gantry.DoneNoToolCalls,
		FinalOutput: "done",
		Usage:       gantry.Usage{InputTokens: 10, OutputTokens: 5},
		Meta:        map[string]any{"note": "string-valued meta round-trips"},
	}

	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got gantry.State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(*orig, got) {
		t.Fatalf("round-trip mismatch:\n orig=%#v\n got =%#v", *orig, got)
	}
}
