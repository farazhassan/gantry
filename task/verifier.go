package task

import "context"

// Verifier decides whether a task that produced a final answer is actually
// done. It is the seam where Phase 3's critic plugs in; Phase 2 ships only the
// default-pass NoopVerifier. A false result means "not done — keep working",
// with reason carried for diagnostics.
type Verifier interface {
	Verify(ctx context.Context, t *Task) (ok bool, reason string)
}

// NoopVerifier always passes. It is the Phase 2 default, making a task's first
// final answer also its completion. Swap it via WithVerifier once a real
// verifier exists.
type NoopVerifier struct{}

// Verify always reports success.
func (NoopVerifier) Verify(context.Context, *Task) (bool, string) { return true, "" }
