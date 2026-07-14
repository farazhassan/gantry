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

// TaskStatusTool is a read-only server-side tool that reports one task's
// current status, its result summary (task.Result), and the budget it has
// consumed. It takes an explicit task id, so it works for the session's own
// tasks and for spawned children whose ids came back from list_tasks or the
// spawn tools. The TaskStore is injected at construction; the tool never writes.
type TaskStatusTool struct {
	tasks task.TaskStore
}

// NewTaskStatusTool builds the tool over the same TaskStore the Driver persists
// through. Register it on the task-executing agent via tool.FromTools (or a
// caller-owned tool.Registry). Panics if tasks is nil.
func NewTaskStatusTool(tasks task.TaskStore) *TaskStatusTool {
	if tasks == nil {
		panic("taskmanager: NewTaskStatusTool requires a non-nil TaskStore")
	}
	return &TaskStatusTool{tasks: tasks}
}

// compile-time check: TaskStatusTool implements tool.Tool.
var _ tool.Tool = (*TaskStatusTool)(nil)

// Definition describes the tool to the model.
func (t *TaskStatusTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name: "task_status",
		Description: "Look up one task by id: its current status " +
			"(pending/active/awaiting_input/done/failed/cancelled), the final " +
			"result text so far (empty if none yet), and the budget consumed " +
			"(runs, tokens, cost).",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "task_id": {"type": "string", "description": "The id of the task to inspect."}
  },
  "required": ["task_id"]
}`),
	}
}

// Invoke loads the task and returns its status report. It returns a tool error
// (surfaced to the model; run continues) when the input is malformed, the id is
// empty, or no task exists for the id.
func (t *TaskStatusTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("task_status: invalid input: %w", err)
	}
	if in.TaskID == "" {
		return nil, errors.New("task_status: task_id is required")
	}
	tk, err := t.tasks.LoadTask(ctx, in.TaskID)
	if errors.Is(err, task.ErrNotFound) {
		return nil, fmt.Errorf("task_status: no task with id %q", in.TaskID)
	}
	if err != nil {
		return nil, fmt.Errorf("task_status: load task: %w", err)
	}

	out := struct {
		ID           string  `json:"id"`
		Title        string  `json:"title,omitempty"`
		Status       string  `json:"status"`
		Result       string  `json:"result,omitempty"`
		RunsUsed     int     `json:"runs_used"`
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		CostUSD      float64 `json:"cost_usd"`
	}{
		ID:           tk.ID,
		Title:        tk.Title,
		Status:       string(tk.Status),
		Result:       task.Result(tk),
		RunsUsed:     tk.Budget.UsedRuns,
		InputTokens:  tk.Budget.UsedUsage.InputTokens,
		OutputTokens: tk.Budget.UsedUsage.OutputTokens,
		CostUSD:      tk.Budget.UsedUsage.Cost,
	}
	return json.Marshal(out)
}
