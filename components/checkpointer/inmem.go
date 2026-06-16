package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/farazhassan/gantry"
)

// InMemoryCheckpointer is a process-local store. Useful for tests; not
// suitable for production resume across processes (use a Redis/SQL adapter).
type InMemoryCheckpointer struct {
	mu     sync.Mutex
	states map[string]*gantry.State
}

// NewInMemory returns an empty store.
func NewInMemory() *InMemoryCheckpointer {
	return &InMemoryCheckpointer{states: map[string]*gantry.State{}}
}

func (c *InMemoryCheckpointer) Save(_ context.Context, id string, state *gantry.State) error {
	// Reject nil for parity with FileCheckpointer: a nil state signals an
	// upstream bug and would otherwise panic on the dereference below.
	if state == nil {
		return errors.New("checkpointer: Save requires a non-nil state")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Shallow copy is sufficient because callers stop using state after PhaseEnd.
	copied := *state
	c.states[id] = &copied
	return nil
}

func (c *InMemoryCheckpointer) Load(_ context.Context, id string) (*gantry.State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.states[id]
	if !ok {
		return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	copied := *s
	return &copied, nil
}
