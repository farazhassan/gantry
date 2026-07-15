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
			"context with the current conversation). The work runs independently. " +
			"Returns the new session and task ids; the ids are provisional until " +
			"this run commits — if the run errors, the spawn is discarded.",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the spawned session's task should accomplish."},
    "title": {"type": "string", "description": "Optional short title."},
    "agent": {"type": "string", "description": "Optional agent profile (registry key) to run the spawned task under. Omit for the default agent."}
  },
  "required": ["goal"]
}`),
	}
}

// Invoke decodes the request, mints the session + task ids via the ctx-carried
// collector's new-session buffer, and returns them. It returns a tool error
// (surfaced to the model; run continues) when the input is malformed, the goal
// is empty, no collector is present (tool used outside a task-driven run), or
// the spawn would exceed the policy's max depth.
func (t *SpawnSessionTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal  string `json:"goal"`
		Title string `json:"title"`
		Agent string `json:"agent"`
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
	sid, tid, err := coll.addSession(in.Goal, in.Title, in.Agent)
	if err != nil {
		return nil, fmt.Errorf("spawn_session: %w", err)
	}
	out, err := json.Marshal(struct {
		Spawned   bool   `json:"spawned"`
		SessionID string `json:"session_id"`
		TaskID    string `json:"task_id"`
	}{Spawned: true, SessionID: sid, TaskID: tid})
	if err != nil {
		return nil, fmt.Errorf("spawn_session: encode result: %w", err)
	}
	return out, nil
}
