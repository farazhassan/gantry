package taskmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// SpawnSessionTool is a server-side tool that lets a running task spawn
// UNRELATED work in a brand-new session (no shared chat context). It buffers the
// request into the per-Advance spawnCollector carried on ctx; the TaskManager
// mints a fresh session + task and persists them after the run returns, then
// enqueues the new session id onto the ReadyQueue. The tool itself touches no
// store. Distinct verb from create_task, which queues a follow-on task in the
// SAME session.
type SpawnSessionTool struct{}

// NewSpawnSessionTool builds the tool. Register it on the task-executing agent
// at build time via tool.WithTools.
func NewSpawnSessionTool() *SpawnSessionTool { return &SpawnSessionTool{} }

// compile-time check: SpawnSessionTool implements tool.Tool.
var _ tool.Tool = (*SpawnSessionTool)(nil)

// Definition describes the tool to the model.
func (t *SpawnSessionTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name: "spawn_session",
		Description: "Spawn unrelated work in a brand-new session (no shared " +
			"context with the current conversation). The work runs independently.",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the spawned session's task should accomplish."},
    "title": {"type": "string", "description": "Optional short title."}
  },
  "required": ["goal"]
}`),
	}
}

// Invoke decodes the request and buffers it into the ctx-carried collector's
// new-session buffer. It returns a tool error (surfaced to the model; run
// continues) when the input is malformed, the goal is empty, or no collector is
// present (tool used outside a task-driven run). The new session id is minted
// after the run, so it cannot be returned here — hence {"spawned": true}.
func (t *SpawnSessionTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal  string `json:"goal"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("spawn_session: invalid input: %w", err)
	}
	if in.Goal == "" {
		return nil, errors.New("spawn_session: goal is required")
	}
	coll, ok := collectorFrom(ctx)
	if !ok {
		return nil, errors.New("spawn_session: not available outside a task-driven run")
	}
	coll.addSession(in.Goal, in.Title)
	return json.RawMessage(`{"spawned": true}`), nil
}
