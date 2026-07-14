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
			"queued and runs after the active task completes. Returns the new " +
			"task's id (provisional until this run commits — if the run errors, " +
			"the spawn is discarded). Pass depends_on to gate it on earlier " +
			"tasks: it runs only after every listed task is done, and is " +
			"cancelled if any of them fails or is cancelled. depends_on ids must " +
			"be task ids from THIS session (e.g. ids returned by earlier " +
			"create_task calls).",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the spawned task should accomplish."},
    "title": {"type": "string", "description": "Optional short title."},
    "depends_on": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Optional same-session task ids that must complete (done) before this task runs. If a listed task fails or is cancelled, this task is cancelled instead of run."
    }
  },
  "required": ["goal"]
}`),
	}
}

// Invoke decodes the request, mints the task id via the ctx-carried collector,
// buffers the spawn, and returns the id. It returns a tool error (surfaced to
// the model; run continues) when the input is malformed, the goal is empty, a
// depends_on entry is empty, no collector is present (tool used outside a
// task-driven run), or the spawn would exceed the policy's max depth.
// depends_on ids are NOT validated for existence here — the tool has no store
// access by design; the TaskManager validates at drain time and cancels the
// spawn if an id is not a task in this session (Decision I).
func (t *CreateTaskTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal      string   `json:"goal"`
		Title     string   `json:"title"`
		DependsOn []string `json:"depends_on"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("create_task: invalid input: %w", err)
	}
	if in.Goal == "" {
		return nil, errors.New("create_task: goal is required")
	}
	for _, dep := range in.DependsOn {
		if dep == "" {
			return nil, errors.New("create_task: depends_on entries must be non-empty task ids")
		}
	}
	coll, ok := collectorFrom(ctx)
	if !ok {
		return nil, errors.New("create_task: not available outside a task-driven run")
	}
	id, err := coll.add(in.Goal, in.Title, in.DependsOn)
	if err != nil {
		return nil, fmt.Errorf("create_task: %w", err)
	}
	out, err := json.Marshal(struct {
		Queued bool   `json:"queued"`
		TaskID string `json:"task_id"`
	}{Queued: true, TaskID: id})
	if err != nil {
		return nil, fmt.Errorf("create_task: encode result: %w", err)
	}
	return out, nil
}
