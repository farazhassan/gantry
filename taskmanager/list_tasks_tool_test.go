package taskmanager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/task"
)

func TestListTasksToolDefinition(t *testing.T) {
	def := NewListTasksTool(NewInMemoryMetaStore()).Definition()
	if def.Name != "list_tasks" {
		t.Errorf("Name = %q, want list_tasks", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var schema struct {
		Type       string         `json:"type"`
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q, want object", schema.Type)
	}
	if len(schema.Properties) != 0 || len(schema.Required) != 0 {
		t.Errorf("list_tasks takes no input; schema = %+v", schema)
	}
}

func TestListTasksToolReturnsRefsAndChildren(t *testing.T) {
	meta := NewInMemoryMetaStore()
	ctx := context.Background()
	sm := &task.SessionMeta{
		TaskRefs: []task.TaskRef{
			{ID: "t1", Title: "alpha", Status: task.TaskDone},
			{ID: "t2", Status: task.TaskAwaitingInput},
		},
		ActiveTaskID: "t2",
		ChildRefs: []task.ChildRef{
			{SessionID: "child-sess", TaskID: "ct1", Title: "child work"},
		},
	}
	if err := meta.SaveMeta(ctx, "s1", sm); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	tl := NewListTasksTool(meta)
	coll := &spawnCollector{sessionID: "s1", taskID: "t2"}
	out, err := tl.Invoke(withCollector(ctx, coll), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	var got struct {
		Tasks []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"tasks"`
		Children []struct {
			TaskID    string `json:"task_id"`
			SessionID string `json:"session_id"`
			Title     string `json:"title"`
		} `json:"children"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got.Tasks) != 2 {
		t.Fatalf("tasks = %d, want 2", len(got.Tasks))
	}
	if got.Tasks[0].ID != "t1" || got.Tasks[0].Title != "alpha" || got.Tasks[0].Status != "done" {
		t.Errorf("tasks[0] = %+v, want {t1 alpha done}", got.Tasks[0])
	}
	if got.Tasks[1].ID != "t2" || got.Tasks[1].Status != "awaiting_input" {
		t.Errorf("tasks[1] = %+v, want {t2 _ awaiting_input}", got.Tasks[1])
	}
	if len(got.Children) != 1 {
		t.Fatalf("children = %d, want 1", len(got.Children))
	}
	if got.Children[0].TaskID != "ct1" || got.Children[0].SessionID != "child-sess" || got.Children[0].Title != "child work" {
		t.Errorf("children[0] = %+v, want {ct1 child-sess child work}", got.Children[0])
	}
}

func TestListTasksToolUnknownSessionReturnsEmptyList(t *testing.T) {
	tl := NewListTasksTool(NewInMemoryMetaStore())
	coll := &spawnCollector{sessionID: "never-seen", taskID: "t1"}
	out, err := tl.Invoke(withCollector(context.Background(), coll), nil)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var got struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if len(got.Tasks) != 0 {
		t.Errorf("tasks = %d, want 0 for an unknown session", len(got.Tasks))
	}
}

func TestListTasksToolNoCollectorIsError(t *testing.T) {
	tl := NewListTasksTool(NewInMemoryMetaStore())
	if _, err := tl.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Errorf("Invoke without collector = nil error, want an error")
	}
}

func TestNewListTasksToolNilMetaPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("NewListTasksTool(nil) did not panic")
		}
	}()
	NewListTasksTool(nil)
}
