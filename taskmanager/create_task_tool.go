package taskmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// CreateTaskTool is a server-side tool that lets a running task spawn a
// follow-on task in the same session. It buffers the request into the
// per-Advance spawnCollector carried on ctx; the TaskManager mints and persists
// the task after the run returns. The tool itself touches no store.
type CreateTaskTool struct{}

// NewCreateTaskTool builds the tool. Register it on the task-executing agent at
// build time via tool.WithTools.
func NewCreateTaskTool() *CreateTaskTool { return &CreateTaskTool{} }

// compile-time check: CreateTaskTool implements tool.Tool.
var _ tool.Tool = (*CreateTaskTool)(nil)

// Definition describes the tool to the model.
func (t *CreateTaskTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name: "create_task",
		Description: "Spawn a follow-on task in the current session. The task is " +
			"queued and runs after the active task completes.",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the spawned task should accomplish."},
    "title": {"type": "string", "description": "Optional short title."}
  },
  "required": ["goal"]
}`),
	}
}

// Invoke decodes the request and buffers it into the ctx-carried collector. It
// returns a tool error (surfaced to the model; run continues) when the input is
// malformed, the goal is empty, or no collector is present (tool used outside a
// task-driven run).
func (t *CreateTaskTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal  string `json:"goal"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("create_task: invalid input: %w", err)
	}
	if in.Goal == "" {
		return nil, errors.New("create_task: goal is required")
	}
	coll, ok := collectorFrom(ctx)
	if !ok {
		return nil, errors.New("create_task: not available outside a task-driven run")
	}
	coll.add(in.Goal, in.Title)
	return json.RawMessage(`{"queued": true}`), nil
}
