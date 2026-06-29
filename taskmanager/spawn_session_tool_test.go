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
	if len(schema.Required) != 1 || schema.Required[0] != "goal" {
		t.Errorf("required = %v, want [goal]", schema.Required)
	}
}

func TestSpawnSessionToolBuffersIntoSessionBuffer(t *testing.T) {
	coll := &spawnCollector{}
	ctx := withCollector(context.Background(), coll)
	tool := NewSpawnSessionTool()

	out, err := tool.Invoke(ctx, json.RawMessage(`{"goal":"do x","title":"X"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(string(out), `"spawned"`) {
		t.Errorf("out = %s, want spawned:true", out)
	}
	// Lands in the session buffer, NOT the same-session goals buffer.
	if got := coll.drain(); len(got) != 0 {
		t.Errorf("goals buffer = %+v, want empty", got)
	}
	sess := coll.drainSessions()
	if len(sess) != 1 || sess[0].goal != "do x" || sess[0].title != "X" {
		t.Errorf("sessions buffer = %+v, want one {do x, X}", sess)
	}
}

func TestSpawnSessionToolErrors(t *testing.T) {
	tool := NewSpawnSessionTool()
	collCtx := withCollector(context.Background(), &spawnCollector{})

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
