package eval

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// AgentFactory produces a fresh *harness.Agent per call. Returning fresh
// agents ensures that per-case state (Memory, etc.) does not leak across
// evaluations.
type AgentFactory func(ctx context.Context) (*harness.Agent, error)

// Config pairs a Name (for reporting) with an AgentFactory.
type Config struct {
	Name    string
	Factory AgentFactory
}
