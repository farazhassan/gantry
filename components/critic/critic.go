// Package critic defines the Critic interface for self-review.
package critic

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// Verdict is the outcome of a Critique.
type Verdict struct {
	Accept       bool
	Reason       string
	ModifyOutput string // optional rewrite; empty = no rewrite
}

// Critic reviews state.LastResponse and returns a Verdict.
type Critic interface {
	Critique(ctx context.Context, state *harness.State) (Verdict, error)
}
