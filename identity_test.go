package gantry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEventIdentityFieldsJSONRoundTrip(t *testing.T) {
	in := Event{
		Type:      EventDone,
		RunID:     "run-0011223344556677",
		SessionID: "sess-1",
		TaskID:    "task-1",
		Agent:     "router",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"run_id":"run-0011223344556677"`,
		`"session_id":"sess-1"`,
		`"task_id":"task-1"`,
		`"agent":"router"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Event JSON %s missing %s", b, want)
		}
	}
	var out Event
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEventIdentityFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Event{Type: EventDone})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, bad := range []string{`"run_id"`, `"session_id"`, `"task_id"`, `"agent"`} {
		if strings.Contains(string(b), bad) {
			t.Errorf("Event JSON %s leaked empty identity key %s", b, bad)
		}
	}
}

func TestEventParentFieldsJSONRoundTrip(t *testing.T) {
	in := Event{
		Type:             EventDone,
		RunID:            "run-child",
		Agent:            "investigation",
		ParentRunID:      "run-parent",
		ParentToolCallID: "call-1",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		`"parent_run_id":"run-parent"`,
		`"parent_tool_call_id":"call-1"`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Event JSON %s missing %s", b, want)
		}
	}
	var out Event
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestEventParentFieldsOmittedWhenEmpty(t *testing.T) {
	b, err := json.Marshal(Event{Type: EventDone})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, bad := range []string{`"parent_run_id"`, `"parent_tool_call_id"`} {
		if strings.Contains(string(b), bad) {
			t.Errorf("Event JSON %s leaked empty parent-link key %s", b, bad)
		}
	}
}

func TestWithNameSetsAgentName(t *testing.T) {
	a, err := NewAgent(WithLLM(stubLLM{}), WithName("router"))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if got := a.Name(); got != "router" {
		t.Errorf("Name() = %q, want %q", got, "router")
	}
}

func TestNameDefaultsEmpty(t *testing.T) {
	a, err := NewAgent(WithLLM(stubLLM{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if got := a.Name(); got != "" {
		t.Errorf("Name() = %q, want empty", got)
	}
}
