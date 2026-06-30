package checkpointer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

// StoreCheckpointer is a Checkpointer that serializes *gantry.State (with optional
// field projection) and persists the bytes via an injected Store.
type StoreCheckpointer struct {
	store Store
	proj  projection
}

// FromStore builds a Checkpointer backed by store. Options configure field
// projection (StoreOnly / Omit, mutually exclusive). It is named FromStore
// because checkpointer.New is the component wirer (see middleware.go).
func FromStore(store Store, opts ...Option) (*StoreCheckpointer, error) {
	if store == nil {
		return nil, errors.New("checkpointer: FromStore requires a non-nil store")
	}
	var c config
	for _, o := range opts {
		o(&c)
	}
	if c.projOpts > 1 {
		return nil, errors.New("checkpointer: conflicting projection options (use at most one of StoreOnly/Omit)")
	}
	return &StoreCheckpointer{store: store, proj: c.proj}, nil
}

func (c *StoreCheckpointer) Save(ctx context.Context, id string, state *gantry.State) error {
	if state == nil {
		return errors.New("checkpointer: Save requires a non-nil state")
	}
	blob, err := c.proj.marshal(state)
	if err != nil {
		return fmt.Errorf("checkpointer: marshal %q: %w", id, err)
	}
	return c.store.Put(ctx, id, blob)
}

func (c *StoreCheckpointer) Load(ctx context.Context, id string) (*gantry.State, error) {
	blob, found, err := c.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: id %q", ErrNotFound, id)
	}
	var state gantry.State
	if err := json.Unmarshal(blob, &state); err != nil {
		return nil, fmt.Errorf("checkpointer: unmarshal %q: %w", id, err)
	}
	return &state, nil
}

var _ Checkpointer = (*StoreCheckpointer)(nil)
