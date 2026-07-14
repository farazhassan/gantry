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
	if !strings.Contains(def.Description, "provisional") {
		t.Errorf("Description = %q, want it to state the returned id is provisional until the run commits", def.Description)
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

func TestCreateTaskToolInvokeReturnsMintedID(t *testing.T) {
	coll := newTestCollector()
	ctx := withCollector(context.Background(), coll)
	tool := NewCreateTaskTool()

	out, err := tool.Invoke(ctx, json.RawMessage(`{"goal":"write docs","title":"docs"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res struct {
		Queued bool   `json:"queued"`
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("output not valid JSON: %v (%s)", err, out)
	}
	if !res.Queued || res.TaskID != "task-1" {
		t.Errorf("res = %+v, want {Queued:true TaskID:task-1}", res)
	}
	reqs := coll.drain()
	if len(reqs) != 1 || reqs[0] != (spawnReq{goal: "write docs", title: "docs", taskID: "task-1"}) {
		t.Errorf("buffered = %+v, want one {write docs docs task-1}", reqs)
	}
}

func TestCreateTaskToolDepthExceededIsToolError(t *testing.T) {
	coll := newTestCollector()
	coll.parentDepth = DefaultMaxSpawnDepth
	ctx := withCollector(context.Background(), coll)

	_, err := NewCreateTaskTool().Invoke(ctx, json.RawMessage(`{"goal":"x"}`))
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Errorf("err = %v, want a depth error", err)
	}
	if got := coll.drain(); len(got) != 0 {
		t.Errorf("rejected spawn buffered something: %+v", got)
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
	coll := newTestCollector()
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
	coll := newTestCollector()
	ctx := withCollector(context.Background(), coll)
	tool := NewCreateTaskTool()

	if _, err := tool.Invoke(ctx, json.RawMessage(`not json`)); err == nil {
		t.Errorf("Invoke with malformed input = nil error, want an error")
	}
}
