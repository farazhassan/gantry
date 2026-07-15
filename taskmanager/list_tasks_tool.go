package taskmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/task"
)

// ListTasksTool is a read-only server-side tool that lists the current
// session's tasks (from SessionMeta.TaskRefs) and the child sessions it spawned
// (from SessionMeta.ChildRefs). Unlike the spawn tools it holds a store — the
// MetaStore is injected at construction — but it never writes. The current
// session id is resolved from the per-Advance collector on ctx, so the tool
// only works inside a task-driven run.
type ListTasksTool struct {
	meta MetaStore
}

// NewListTasksTool builds the tool over the same MetaStore the TaskManager
// uses. Register it on the task-executing agent via tool.FromTools (or a
// caller-owned tool.Registry). Panics if meta is nil.
func NewListTasksTool(meta MetaStore) *ListTasksTool {
	if meta == nil {
		panic("taskmanager: NewListTasksTool requires a non-nil MetaStore")
	}
	return &ListTasksTool{meta: meta}
}

// compile-time check: ListTasksTool implements tool.Tool.
var _ tool.Tool = (*ListTasksTool)(nil)

// Definition describes the tool to the model.
func (t *ListTasksTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name: "list_tasks",
		Description: "List this session's tasks (id, title, status) and any child " +
			"sessions it spawned (task_id, session_id, title). Child entries carry " +
			"no status here — call task_status with a task_id for current status " +
			"and result. Takes no input.",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {}
}`),
	}
}

// listedTask is one own-session entry in the tool output.
type listedTask struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
}

// listedChild is one spawned-child entry in the tool output.
type listedChild struct {
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
}

// Invoke resolves the current session from the ctx collector, loads its meta,
// and returns the task list. A session with no meta yet yields empty lists. It
// returns a tool error (surfaced to the model; run continues) when no collector
// is present or the store fails.
func (t *ListTasksTool) Invoke(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	coll, ok := collectorFrom(ctx)
	if !ok || coll.sessionID == "" {
		return nil, errors.New("list_tasks: not available outside a task-driven run")
	}
	sm, err := t.meta.LoadMeta(ctx, coll.sessionID)
	if errors.Is(err, ErrMetaNotFound) {
		sm = &task.SessionMeta{}
	} else if err != nil {
		return nil, fmt.Errorf("list_tasks: load meta: %w", err)
	}

	out := struct {
		Tasks    []listedTask  `json:"tasks"`
		Children []listedChild `json:"children,omitempty"`
	}{Tasks: make([]listedTask, 0, len(sm.TaskRefs))}

	for _, ref := range sm.TaskRefs {
		out.Tasks = append(out.Tasks, listedTask{
			ID:     ref.ID,
			Title:  ref.Title,
			Status: string(ref.Status),
		})
	}
	for _, ch := range sm.ChildRefs {
		out.Children = append(out.Children, listedChild{
			TaskID:    ch.TaskID,
			SessionID: ch.SessionID,
			Title:     ch.Title,
		})
	}
	return json.Marshal(out)
}
