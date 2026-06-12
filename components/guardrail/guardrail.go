// Package guardrail defines the Guardrail interface for validating inputs
// and outputs.
package guardrail

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// Direction selects whether a Guardrail runs pre-LLM or post-LLM.
type Direction int

const (
	DirectionInput Direction = iota
	DirectionOutput
)

// Guardrail validates state.Messages (DirectionInput) or state.LastResponse
// (DirectionOutput). Return ErrGuardrailBlocked to short-circuit the loop.
type Guardrail interface {
	Check(ctx context.Context, state *harness.State, direction Direction) error
}
