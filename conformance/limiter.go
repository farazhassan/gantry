package conformance

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/limiter"
)

// LimiterSuite verifies the contract of limiter.Limiter.
func LimiterSuite(t *testing.T, factory func() limiter.Limiter) {
	t.Helper()

	t.Run("check_passes_on_fresh_state", func(t *testing.T) {
		l := factory()
		err := l.Check(context.Background(), &gantry.State{})
		if err != nil {
			t.Errorf("Check should pass on fresh state; got %v", err)
		}
	})

	t.Run("record_does_not_panic", func(t *testing.T) {
		l := factory()
		l.Record(context.Background(), gantry.Usage{InputTokens: 10})
	})
}
