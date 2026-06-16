package eval

import (
	"context"

	"github.com/farazhassan/gantry"
)

// AgentFactory produces a fresh *gantry.Agent per call. Returning fresh
// agents ensures that per-case state (Memory, etc.) does not leak across
// evaluations.
type AgentFactory func(ctx context.Context) (*gantry.Agent, error)

// Config pairs a Name (for reporting) with an AgentFactory.
type Config struct {
	Name    string
	Factory AgentFactory
}
