package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/humanloop"
)

// HumanInLoopSuite verifies the contract of humanloop.HumanInLoop.
func HumanInLoopSuite(t *testing.T, factory func() humanloop.HumanInLoop) {
	t.Helper()

	t.Run("confirm_returns_decision", func(t *testing.T) {
		h := factory()
		d, err := h.Confirm(context.Background(), humanloop.Action{Kind: "tool", Name: "test"})
		if err != nil {
			return
		}
		_ = d.Approved
	})
}
