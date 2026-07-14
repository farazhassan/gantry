package taskmanager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSpawnSessionToolDefinition(t *testing.T) {
	def := NewSpawnSessionTool().Definition()
	if def.Name != "spawn_session" {
		t.Errorf("Name = %q, want spawn_session", def.Name)
	}
	if !strings.Contains(def.Description, "provisional") {
		t.Errorf("Description = %q, want it to state the returned ids are provisional until the run commits", def.Description)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	if _, ok := schema.Properties["goal"]; !ok {
		t.Errorf("schema missing goal property")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Errorf("schema missing title property")
	}
	if _, ok := schema.Properties["agent"]; !ok {
		t.Errorf("schema missing agent property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "goal" {
		t.Errorf("required = %v, want [goal]", schema.Required)
	}
}

func TestSpawnSessionToolInvokeReturnsMintedIDs(t *testing.T) {
	coll := newTestCollector()
	ctx := withCollector(context.Background(), coll)
	tool := NewSpawnSessionTool()

	out, err := tool.Invoke(ctx, json.RawMessage(`{"goal":"do x","title":"X","agent":"researcher"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res struct {
		Spawned   bool   `json:"spawned"`
		SessionID string `json:"session_id"`
		TaskID    string `json:"task_id"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, out)
	}
	if !res.Spawned || res.SessionID != "sess-1" || res.TaskID != "task-1" {
		t.Errorf("res = %+v, want {Spawned:true SessionID:sess-1 TaskID:task-1}", res)
	}
	// Lands in the session buffer, NOT the same-session goals buffer.
	if got := coll.drain(); len(got) != 0 {
		t.Errorf("goals buffer = %+v, want empty", got)
	}
	sess := coll.drainSessions()
	if len(sess) != 1 || sess[0] != (spawnReq{goal: "do x", title: "X", taskID: "task-1", sessionID: "sess-1", agent: "researcher"}) {
		t.Errorf("sessions buffer = %+v, want one {do x X task-1 sess-1 researcher}", sess)
	}
}

func TestSpawnSessionToolAgentDefaultsEmpty(t *testing.T) {
	coll := newTestCollector()
	ctx := withCollector(context.Background(), coll)

	if _, err := NewSpawnSessionTool().Invoke(ctx, json.RawMessage(`{"goal":"do x"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	sess := coll.drainSessions()
	if len(sess) != 1 || sess[0].agent != "" {
		t.Errorf("sessions buffer = %+v, want one entry with empty agent (default runner)", sess)
	}
}

func TestSpawnSessionToolDepthExceededIsToolError(t *testing.T) {
	coll := newTestCollector()
	coll.parentDepth = DefaultMaxSpawnDepth
	ctx := withCollector(context.Background(), coll)

	_, err := NewSpawnSessionTool().Invoke(ctx, json.RawMessage(`{"goal":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("err = %v, want a depth error", err)
	}
	if got := coll.drainSessions(); len(got) != 0 {
		t.Errorf("rejected spawn buffered something: %+v", got)
	}
}

func TestSpawnSessionToolErrors(t *testing.T) {
	tool := NewSpawnSessionTool()
	collCtx := withCollector(context.Background(), newTestCollector())

	if _, err := tool.Invoke(collCtx, json.RawMessage(`{`)); err == nil {
		t.Error("malformed JSON: err = nil, want error")
	}
	if _, err := tool.Invoke(collCtx, json.RawMessage(`{"title":"x"}`)); err == nil {
		t.Error("empty goal: err = nil, want error")
	}
	if _, err := tool.Invoke(context.Background(), json.RawMessage(`{"goal":"x"}`)); err == nil {
		t.Error("no collector: err = nil, want error")
	}
}
