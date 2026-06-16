package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/farazhassan/gantry"
)

// Registry is a name-keyed collection of Tools. Safe for concurrent use.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{tools: map[string]Tool{}}
}

// Add registers a tool. If the tool name already exists, it is replaced.
func (r *Registry) Add(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[t.Definition().Name] = t
}

// Lookup returns the tool with the given name.
func (r *Registry) Lookup(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Definitions returns the ToolDef for every registered tool, sorted by
// name for deterministic LLM prompts.
func (r *Registry) Definitions() []gantry.ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	// Simple insertion sort for stability (tool count is small).
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j-1] > names[j]; j-- {
			names[j-1], names[j] = names[j], names[j-1]
		}
	}
	out := make([]gantry.ToolDef, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n].Definition())
	}
	return out
}

// Invoke runs the tool referenced by the call. Errors are wrapped with
// gantry.ErrToolExecution.
func (r *Registry) Invoke(ctx context.Context, call gantry.ToolCall) (json.RawMessage, error) {
	t, ok := r.Lookup(call.Name)
	if !ok {
		return nil, fmt.Errorf("%w: unknown tool %q", gantry.ErrToolExecution, call.Name)
	}
	out, err := t.Invoke(ctx, call.Input)
	if err != nil {
		return out, fmt.Errorf("%w: %v", gantry.ErrToolExecution, err)
	}
	return out, nil
}
