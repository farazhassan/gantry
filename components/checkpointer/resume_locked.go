package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/farazhassan/gantry"
)

// ResumeLocked acquires a Lease on id, loads it from cp under that lease,
// then resumes the run on a under a background-renewed lease, and finally
// releases the lease. It returns ErrLeaseHeld immediately if another worker
// currently holds id — no retry/backoff is built in; callers decide whether
// and how to retry. If the lease is lost mid-run (e.g. this worker stalled
// long enough that another worker's TTL-expiry takeover raced it), the
// in-flight a.Resume is cancelled via ctx rather than left to run un-owned.
//
// Acquire runs before Load deliberately: if Load ran first, the *State it
// returns would be captured before this call actually held the lease,
// leaving a window where a concurrent worker's entire resume-and-checkpoint
// cycle (acquire, run, save a newer checkpoint, release) could complete in
// between — and this call would then resume from that now-stale snapshot,
// clobbering the newer one, with the lease's mutual exclusion never having
// protected against it. Loading only after Acquire succeeds guarantees the
// loaded state reflects everything saved before this call actually won the
// lease.
//
// The mid-run checkpointer component (see New's extraPhases) must already be
// installed on a, separately from this call — ResumeLocked only manages the
// lease around a.Resume; it does not itself save state.
func ResumeLocked(ctx context.Context, a *gantry.Agent, cp Checkpointer, lease Lease, id string, ttl time.Duration) (*gantry.State, error) {
	token, err := lease.Acquire(ctx, id, ttl)
	if err != nil {
		return nil, err
	}

	state, err := cp.Load(ctx, id)
	if err != nil {
		_ = lease.Release(context.WithoutCancel(ctx), id, token) // best-effort; it also expires on its own via ttl
		return nil, fmt.Errorf("checkpointer: ResumeLocked load %q: %w", id, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	lost, stop := KeepAlive(runCtx, lease, id, token, ttl)
	go func() {
		select {
		case <-lost:
			cancel()
		case <-runCtx.Done():
		}
	}()

	result, runErr := a.Resume(runCtx, state)
	// a.Resume has already returned, so cancelling runCtx now can't affect
	// it — but it's what unblocks a KeepAlive Renew call that's currently
	// in flight, which stop() otherwise has no way to interrupt: closing
	// stop's internal channel only stops *future* ticks, it can't reach
	// into an already-running Renew call.
	cancel()
	stop()

	if releaseErr := lease.Release(context.WithoutCancel(ctx), id, token); releaseErr != nil && !errors.Is(releaseErr, ErrLeaseLost) {
		if result != nil && result.Trace != nil {
			result.Trace.Record(gantry.TraceEvent{
				Name:  "lease_release_failed",
				Kind:  gantry.KindEvent,
				Err:   releaseErr,
				Attrs: map[string]any{"id": id},
			})
		}
	}

	return result, runErr
}
