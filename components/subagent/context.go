// Package subagent provides an agent-as-tool primitive. New wraps a child
// *gantry.Agent as a tool.Tool that runs the child inline on Invoke —
// a fresh, context-isolated run seeded only with the goal and an optional
// briefing — and returns the child's FinalOutput as the tool result.
// Component wires delegate tools (plus any plain tools) into a parent agent
// and folds child-run usage into the parent run's State.Usage via a
// ctx-injected accumulator. A ctx-carried depth counter guards against
// unbounded sub-agent recursion.
package subagent

import (
	"context"
	"sync"

	"github.com/farazhassan/gantry"
)

// depthKey is the unexported context key carrying the sub-agent nesting depth.
type depthKey struct{}

// withDepth returns a ctx recording d as the current sub-agent depth.
func withDepth(ctx context.Context, d int) context.Context {
	return context.WithValue(ctx, depthKey{}, d)
}

// depthFrom reports the sub-agent depth carried by ctx; 0 when absent
// (a top-level run).
func depthFrom(ctx context.Context) int {
	d, _ := ctx.Value(depthKey{}).(int)
	return d
}

// usageRecorder accumulates child-run usage across the delegate tools invoked
// during one PhaseToolExec pass. Safe for concurrent use: parallel tool
// dispatch may run several delegate tools at once.
type usageRecorder struct {
	mu sync.Mutex
	u  gantry.Usage
}

// add folds u into the running total.
func (r *usageRecorder) add(u gantry.Usage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.u = r.u.Add(u)
}

// total returns the accumulated usage.
func (r *usageRecorder) total() gantry.Usage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.u
}

// usageKey is the unexported context key for the per-dispatch recorder.
type usageKey struct{}

// withUsageRecorder returns a ctx carrying rec. Component's usage_fold
// middleware installs a fresh recorder before delegating to tool dispatch;
// delegate tools record their child run's usage into it.
func withUsageRecorder(ctx context.Context, rec *usageRecorder) context.Context {
	return context.WithValue(ctx, usageKey{}, rec)
}

// usageRecorderFrom extracts the recorder from ctx, reporting whether one is
// set. Absent (ok == false) when the agent was wired with plain
// tool.FromTools instead of subagent.Component.
func usageRecorderFrom(ctx context.Context) (*usageRecorder, bool) {
	rec, ok := ctx.Value(usageKey{}).(*usageRecorder)
	return rec, ok
}
