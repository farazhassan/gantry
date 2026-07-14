package router

import (
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

// Registry is an ordered route table mapping stable string keys to agents.
// Keys keep insertion order so classifier prompts and UIs list routes
// deterministically.
//
// Build the table up front and treat it as read-only afterwards — like
// gantry.Agent, it is configured at startup and is not synchronized for
// concurrent mutation.
type Registry struct {
	keys    []string
	entries map[string]entry
}

type entry struct {
	description string
	agent       *gantry.Agent
}

// NewRegistry returns an empty route table.
func NewRegistry() *Registry {
	return &Registry{entries: map[string]entry{}}
}

// Add registers agent under key. The description is what LLMRouter shows the
// classifier model, so write it like a tool description: one or two sentences
// on what requests this agent handles. Add rejects empty keys, nil agents,
// and duplicate keys.
func (r *Registry) Add(key, description string, agent *gantry.Agent) error {
	if key == "" {
		return errors.New("router: route key must be non-empty")
	}
	if agent == nil {
		return fmt.Errorf("router: route %q has a nil agent", key)
	}
	if _, dup := r.entries[key]; dup {
		return fmt.Errorf("router: duplicate route key %q", key)
	}
	r.entries[key] = entry{description: description, agent: agent}
	r.keys = append(r.keys, key)
	return nil
}

// Get returns the agent registered under key.
func (r *Registry) Get(key string) (*gantry.Agent, bool) {
	e, ok := r.entries[key]
	if !ok {
		return nil, false
	}
	return e.agent, true
}

// Keys returns the route keys in insertion order. The slice is a copy.
func (r *Registry) Keys() []string {
	out := make([]string, len(r.keys))
	copy(out, r.keys)
	return out
}

// Description returns the description registered under key.
func (r *Registry) Description(key string) (string, bool) {
	e, ok := r.entries[key]
	if !ok {
		return "", false
	}
	return e.description, true
}
