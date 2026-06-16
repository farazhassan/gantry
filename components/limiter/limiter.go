// Package limiter defines the Limiter interface for capping token, cost,
// and iteration usage.
package limiter

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Limiter is queried before LLM and tool calls and records usage after.
type Limiter interface {
	Check(ctx context.Context, state *gantry.State) error
	Record(ctx context.Context, usage gantry.Usage)
}
