// Package skill defines the Skill interface for conditional system-prompt
// injection.
package skill

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// Skill is a chunk of conditional instructions or context. When Matches
// returns true during PhaseAssembleContext, SystemPrompt is appended to
// state.System (joined by newlines).
type Skill interface {
	Name() string
	SystemPrompt() string
	Matches(ctx context.Context, state *harness.State) bool
}
