package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
)

// CriticSuite verifies the contract of critic.Critic.
func CriticSuite(t *testing.T, factory func() critic.Critic) {
	t.Helper()

	t.Run("critique_returns_verdict", func(t *testing.T) {
		c := factory()
		state := &gantry.State{LastResponse: &gantry.LLMResponse{Content: "candidate"}}
		v, err := c.Critique(context.Background(), state)
		if err != nil {
			t.Fatalf("Critique: %v", err)
		}
		// Verdict is a struct value; nothing to nil-check.
		_ = v
	})

	t.Run("critique_with_nil_lastresponse_does_not_panic", func(t *testing.T) {
		c := factory()
		_, err := c.Critique(context.Background(), &gantry.State{})
		_ = err
	})
}
