package harness_test

import (
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestToolCallZeroValue(t *testing.T) {
	var tc harness.ToolCall
	if tc.ID != "" || tc.Name != "" || tc.Input != nil {
		t.Errorf("zero ToolCall non-empty: %+v", tc)
	}
}

func TestToolResultIsError(t *testing.T) {
	r := harness.ToolResult{CallID: "abc", Content: "fail", IsError: true}
	if !r.IsError {
		t.Error("IsError should be true")
	}
	if r.CallID != "abc" {
		t.Errorf("CallID = %q, want %q", r.CallID, "abc")
	}
}

func TestToolDefSchemaIsJSON(t *testing.T) {
	def := harness.ToolDef{
		Name:        "search",
		Description: "Searches the web",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}
	var m map[string]any
	if err := json.Unmarshal(def.Schema, &m); err != nil {
		t.Errorf("Schema is not valid JSON: %v", err)
	}
}
