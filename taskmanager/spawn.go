package taskmanager

import (
	"context"
	"fmt"
	"sync"

	"github.com/farazhassan/gantry/task"
)

// spawnReq is a single buffered request to create a follow-on task. IDs are
// minted eagerly at tool-Invoke time (so the tool can return them to the
// model) and consumed verbatim by enqueueSpawns after the run returns.
// sessionID is set only for new-session (spawn_session) requests, as is agent —
// the registry key of the runner profile the spawned task should run under
// ("" ⇒ default runner).
type spawnReq struct {
	goal      string
	title     string
	taskID    string
	sessionID string
	agent     string
}

// spawnCollector buffers spawn requests during one Advance. It carries the
// manager's id minters plus the parent task's depth and the spawn policy's
// resolved max depth, so id minting and depth gating happen at Invoke time. It
// never persists anything: eagerly-minted ids are provisional until the
// orchestrator drains the collector after a successful run (errored runs
// discard the buffer, abandoning the ids). Built via TaskManager.newCollector
// in drive; construct test instances with all four config fields set.
//
// sessionID/taskID identify the run the collector was built for. They are set
// once at construction on the orchestrator goroutine and never mutated, so
// tools may read them concurrently without holding mu. They let read-only
// tools (list_tasks) resolve "the current session" from ctx alone.
type spawnCollector struct {
	sessionID string
	taskID    string

	newTaskID    func() string
	newSessionID func() string
	parentDepth  int
	maxDepth     int

	mu       sync.Mutex
	goals    []spawnReq
	sessions []spawnReq
}

// depthErr returns a model-visible error when one more spawn level would exceed
// the max depth, or nil when the spawn is allowed. Callers hold c.mu.
func (c *spawnCollector) depthErr() error {
	if c.parentDepth+1 > c.maxDepth {
		return fmt.Errorf("spawn depth limit reached (child depth %d exceeds max %d); do the work directly instead of spawning",
			c.parentDepth+1, c.maxDepth)
	}
	return nil
}

// add mints a task id, buffers a same-session spawn request, and returns the
// id. It errors (without buffering) when the spawn would exceed the max depth.
// Safe for concurrent use (the run goroutine may invoke tools from parallel
// tool dispatch).
func (c *spawnCollector) add(goal, title string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.depthErr(); err != nil {
		return "", err
	}
	id := c.newTaskID()
	c.goals = append(c.goals, spawnReq{goal: goal, title: title, taskID: id})
	return id, nil
}

// drain returns the buffered same-session requests in FIFO order and clears
// that buffer.
func (c *spawnCollector) drain() []spawnReq {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.goals
	c.goals = nil
	return out
}

// addSession mints a session id and a task id, buffers a new-session spawn
// request, and returns both. agent is the registry key of the runner profile
// the spawned task should run under ("" ⇒ default runner). It errors (without
// buffering) when the spawn would exceed the max depth. Safe for concurrent use.
func (c *spawnCollector) addSession(goal, title, agent string) (sessionID, taskID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.depthErr(); err != nil {
		return "", "", err
	}
	sessionID = c.newSessionID()
	taskID = c.newTaskID()
	c.sessions = append(c.sessions, spawnReq{goal: goal, title: title, taskID: taskID, sessionID: sessionID, agent: agent})
	return sessionID, taskID, nil
}

// drainSessions returns the buffered new-session requests in FIFO order and
// clears that buffer (leaving goals untouched).
func (c *spawnCollector) drainSessions() []spawnReq {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.sessions
	c.sessions = nil
	return out
}

// newCollector builds the per-Advance collector for a run of parent, wiring the
// manager's id minters, resolving the spawn policy's effective max depth, and
// stamping the run's identity (parent's session and task ids).
func (m *TaskManager) newCollector(parent *task.Task) *spawnCollector {
	return &spawnCollector{
		sessionID:    parent.SessionID,
		taskID:       parent.ID,
		newTaskID:    m.newID,
		newSessionID: m.newSessionID,
		parentDepth:  parent.Depth,
		maxDepth:     m.policy.maxDepth(),
	}
}

// spawnCtxKey is the unexported context key for the per-Advance collector.
type spawnCtxKey struct{}

// withCollector returns a ctx carrying the collector, so a server-side tool
// invoked deep inside Advance can reach it.
func withCollector(ctx context.Context, c *spawnCollector) context.Context {
	return context.WithValue(ctx, spawnCtxKey{}, c)
}

// collectorFrom extracts the collector from ctx, reporting whether one is set.
func collectorFrom(ctx context.Context) (*spawnCollector, bool) {
	c, ok := ctx.Value(spawnCtxKey{}).(*spawnCollector)
	return c, ok
}
