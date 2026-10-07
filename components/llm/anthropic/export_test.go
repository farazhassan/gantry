package anthropic

import (
	"testing"
	"time"
)

// SetNowForTest replaces the clock used for failure-cache expiry.
func SetNowForTest(t *testing.T, f func() time.Time) {
	old := now
	now = f
	t.Cleanup(func() { now = old })
}
