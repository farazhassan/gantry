package task

import (
	"context"
	"testing"
)

func TestNoopVerifierAlwaysPasses(t *testing.T) {
	var v Verifier = NoopVerifier{}
	ok, reason := v.Verify(context.Background(), &Task{ID: "tk-1"})
	if !ok {
		t.Errorf("NoopVerifier must pass, got ok=false reason=%q", reason)
	}
	if reason != "" {
		t.Errorf("NoopVerifier reason = %q, want empty", reason)
	}
}
