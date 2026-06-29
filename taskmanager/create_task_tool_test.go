package taskmanager

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateTaskToolDefinition(t *testing.T) {
	def := NewCreateTaskTool().Definition()
	if def.Name != "create_task" {
		t.Errorf("Name = %q, want create_task", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if _, ok := schema.Properties["goal"]; !ok {
		t.Errorf("schema missing 'goal' property")
	}
	if _, ok := schema.Properties["title"]; !ok {
		t.Errorf("schema missing 'title' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "goal" {
		t.Errorf("Required = %v, want [goal]", schema.Required)
	}
}

func TestCreateTaskToolInvokeBuffers(t *testing.T) {
	coll := &spawnCollector{}
	ctx := withCollector(context.Background(), coll)
	tool := NewCreateTaskTool()

	out, err := tool.Invoke(ctx, json.RawMessage(`{"goal":"write docs","title":"docs"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !strings.Contains(string(out), "queued") {
		t.Errorf("output = %s, want a queued confirmation", out)
	}
	reqs := coll.drain()
	if len(reqs) != 1 || reqs[0] != (spawnReq{goal: "write docs", title: "docs"}) {
		t.Errorf("buffered = %+v, want one {write docs, docs}", reqs)
	}
}

func TestCreateTaskToolNoCollectorIsError(t *testing.T) {
	tool := NewCreateTaskTool()
	_, err := tool.Invoke(context.Background(), json.RawMessage(`{"goal":"x"}`))
	if err == nil {
		t.Errorf("Invoke without collector = nil error, want an error")
	}
}

func TestCreateTaskToolEmptyGoalIsError(t *testing.T) {
	coll := &spawnCollector{}
	ctx := withCollector(context.Background(), coll)
	tool := NewCreateTaskTool()

	if _, err := tool.Invoke(ctx, json.RawMessage(`{}`)); err == nil {
		t.Errorf("Invoke with empty goal = nil error, want an error")
	}
	if len(coll.drain()) != 0 {
		t.Errorf("empty-goal Invoke buffered something; want nothing")
	}
}

func TestCreateTaskToolMalformedInputIsError(t *testing.T) {
	coll := &spawnCollector{}
	ctx := withCollector(context.Background(), coll)
	tool := NewCreateTaskTool()

	if _, err := tool.Invoke(ctx, json.RawMessage(`not json`)); err == nil {
		t.Errorf("Invoke with malformed input = nil error, want an error")
	}
}
