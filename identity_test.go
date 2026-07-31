package gantry

import (
	"context"
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

// TestEmitStampsParentLinkFromIdentity is a whitebox test (package gantry,
// not gantry_test) exercising emit() directly: no WithParentLink helper
// exists yet (that lands in Task 2), so this constructs an eventIdentity
// with parent fields set by hand and installs it via withIdentity, the same
// mechanism a.run uses internally.
func TestEmitStampsParentLinkFromIdentity(t *testing.T) {
	id := eventIdentity{
		runID:            "run-child",
		parentRunID:      "p",
		parentToolCallID: "c",
	}
	ctx := withIdentity(context.Background(), id)

	var got Event
	ctx = WithSink(ctx, func(ev Event) error {
		got = ev
		return nil
	})

	if err := emit(ctx, Event{Type: EventDone}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if got.ParentRunID != "p" || got.ParentToolCallID != "c" {
		t.Errorf("emit() stamped ParentRunID/ParentToolCallID = %q/%q, want p/c",
			got.ParentRunID, got.ParentToolCallID)
	}
	if got.RunID != "run-child" {
		t.Errorf("emit() RunID = %q, want run-child", got.RunID)
	}
}

func TestCurrentIdentityReportsAmbientRunAndAgent(t *testing.T) {
	ctx := withIdentity(context.Background(), eventIdentity{runID: "run-x", agent: "orchestrator"})
	runID, agent, ok := CurrentIdentity(ctx)
	if !ok || runID != "run-x" || agent != "orchestrator" {
		t.Errorf("CurrentIdentity = (%q, %q, %v), want (run-x, orchestrator, true)", runID, agent, ok)
	}
}

func TestCurrentIdentityFalseWhenAbsent(t *testing.T) {
	if _, _, ok := CurrentIdentity(context.Background()); ok {
		t.Error("CurrentIdentity on a bare context = ok true, want false")
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
