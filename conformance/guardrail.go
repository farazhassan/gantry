package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/harness"
)

// GuardrailSuite verifies the contract of guardrail.Guardrail.
func GuardrailSuite(t *testing.T, factory func() guardrail.Guardrail) {
	t.Helper()

	t.Run("check_does_not_panic_on_empty_state", func(t *testing.T) {
		g := factory()
		err := g.Check(context.Background(), &harness.State{}, guardrail.DirectionInput)
		_ = err
	})

	t.Run("check_runs_both_directions", func(t *testing.T) {
		g := factory()
		state := &harness.State{LastResponse: &harness.LLMResponse{Content: "anything"}}
		_ = g.Check(context.Background(), state, guardrail.DirectionInput)
		_ = g.Check(context.Background(), state, guardrail.DirectionOutput)
	})
}
