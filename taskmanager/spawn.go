package taskmanager

import (
	"context"
	"sync"
)

// spawnReq is a single buffered request to create a follow-on task.
type spawnReq struct {
	goal  string
	title string
}

// spawnCollector buffers spawn requests during one Advance. It only collects —
// it never mints tasks/sessions or touches any store. A fresh collector is
// created per Advance and drained by the orchestrator after the run returns.
// Two independent buffers: goals = same-session (create_task, 4b); sessions =
// new-session (spawn_session, 4c).
type spawnCollector struct {
	mu       sync.Mutex
	goals    []spawnReq
	sessions []spawnReq
}

// add buffers a spawn request. Safe for concurrent use (the run goroutine may
// invoke the tool from parallel tool dispatch).
func (c *spawnCollector) add(goal, title string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.goals = append(c.goals, spawnReq{goal: goal, title: title})
}

// drain returns the buffered requests in FIFO order and clears the buffer.
func (c *spawnCollector) drain() []spawnReq {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.goals
	c.goals = nil
	return out
}

// addSession buffers a new-session spawn request. Safe for concurrent use.
func (c *spawnCollector) addSession(goal, title string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions = append(c.sessions, spawnReq{goal: goal, title: title})
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
