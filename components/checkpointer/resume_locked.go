package checkpointer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/farazhassan/gantry"
)

// ResumeLocked loads id from cp, acquires a Lease on it, resumes the run on
// a under that background-renewed lease, then releases the lease. It returns
// ErrLeaseHeld immediately if another worker currently holds id — no
// retry/backoff is built in; callers decide whether and how to retry. If
// the lease is lost mid-run (e.g. this worker stalled long enough that
// another worker's TTL-expiry takeover raced it), the in-flight a.Resume is
// cancelled via ctx rather than left to run un-owned.
//
// The mid-run checkpointer component (see New's extraPhases) must already be
// installed on a, separately from this call — ResumeLocked only manages the
// lease around a.Resume; it does not itself save state.
func ResumeLocked(ctx context.Context, a *gantry.Agent, cp Checkpointer, lease Lease, id string, ttl time.Duration) (*gantry.State, error) {
	state, err := cp.Load(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("checkpointer: ResumeLocked load %q: %w", id, err)
	}

	token, err := lease.Acquire(ctx, id, ttl)
	if err != nil {
		return nil, err
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
