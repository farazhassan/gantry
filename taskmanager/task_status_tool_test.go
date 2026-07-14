package taskmanager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

func TestTaskStatusToolDefinition(t *testing.T) {
	def := NewTaskStatusTool(task.NewInMemory()).Definition()
	if def.Name != "task_status" {
		t.Errorf("Name = %q, want task_status", def.Name)
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
	if _, ok := schema.Properties["task_id"]; !ok {
		t.Errorf("schema missing 'task_id' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "task_id" {
		t.Errorf("Required = %v, want [task_id]", schema.Required)
	}
}

func TestTaskStatusToolReportsStatusResultAndBudget(t *testing.T) {
	tasks := task.NewInMemory()
	ctx := context.Background()
	tk := &task.Task{
		ID: "t1", SessionID: "s1", Title: "alpha", Status: task.TaskDone,
		Working: []gantry.Message{
			{Role: gantry.RoleUser, Content: "the goal"},
			{Role: gantry.RoleAssistant, Content: "final answer"},
		},
		Budget: task.TaskBudget{
			UsedRuns:  2,
			UsedUsage: gantry.Usage{InputTokens: 100, OutputTokens: 40, Cost: 0.5},
		},
		CreatedAt: time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}

	out, err := NewTaskStatusTool(tasks).Invoke(ctx, json.RawMessage(`{"task_id":"t1"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var got struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Status       string  `json:"status"`
		Result       string  `json:"result"`
		RunsUsed     int     `json:"runs_used"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.ID != "t1" || got.Title != "alpha" || got.Status != "done" {
		t.Errorf("id/title/status = %q/%q/%q, want t1/alpha/done", got.ID, got.Title, got.Status)
	}
	if got.Result != "final answer" {
		t.Errorf("result = %q, want the task.Result summary", got.Result)
	}
	if got.RunsUsed != 2 || got.InputTokens != 100 || got.OutputTokens != 40 || got.CostUSD != 0.5 {
		t.Errorf("budget = %+v, want {2 100 40 0.5}", got)
	}
}

func TestTaskStatusToolUnknownIDIsError(t *testing.T) {
	if _, err := NewTaskStatusTool(task.NewInMemory()).Invoke(context.Background(), json.RawMessage(`{"task_id":"nope"}`)); err == nil {
		t.Errorf("Invoke with unknown id = nil error, want an error")
	}
}

func TestTaskStatusToolEmptyIDIsError(t *testing.T) {
	if _, err := NewTaskStatusTool(task.NewInMemory()).Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Errorf("Invoke with empty task_id = nil error, want an error")
	}
}

func TestTaskStatusToolMalformedInputIsError(t *testing.T) {
	if _, err := NewTaskStatusTool(task.NewInMemory()).Invoke(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Errorf("Invoke with malformed input = nil error, want an error")
	}
}

func TestNewTaskStatusToolNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("NewTaskStatusTool(nil) did not panic")
		}
	}()
	NewTaskStatusTool(nil)
}
