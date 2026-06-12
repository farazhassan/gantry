// Package checkpointer defines the Checkpointer interface for persisting
// agent state for resume/replay.
package checkpointer

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// Checkpointer persists and restores State by id.
type Checkpointer interface {
	Save(ctx context.Context, id string, state *harness.State) error
	Load(ctx context.Context, id string) (*harness.State, error)
}
