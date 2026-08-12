package checkpointer

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Lease is a distributed mutex keyed by run id, used to ensure only one
// worker resumes a given run at a time. Implementations: components/checkpointer/mem
// (tests/examples), components/checkpointer/redis, components/checkpointer/etcd.
type Lease interface {
	// Acquire claims id for ttl. Returns a token identifying this specific
	// acquisition (a fencing token) for use with Renew/Release. Returns
	// ErrLeaseHeld if another owner currently holds id.
	Acquire(ctx context.Context, id string, ttl time.Duration) (token string, err error)
	// Renew extends a held lease. Returns ErrLeaseLost if token is no
	// longer the current holder (expired, or reclaimed by another worker
	// after expiry).
	Renew(ctx context.Context, id, token string, ttl time.Duration) error
	// Release gives up a held lease early (e.g. clean shutdown or run
	// completion). Best-effort: callers should not treat a Release error as
	// fatal, since the lease expires on its own regardless.
	Release(ctx context.Context, id, token string) error
}

// KeepAlive renews a held lease at ttl/3 intervals until ctx is cancelled or
// the returned stop func is called. If a renewal returns ErrLeaseLost,
// KeepAlive stops renewing and closes lost so the caller can react (e.g.
// cancel an in-flight Resume). Renewal errors other than ErrLeaseLost are
// treated as transient and retried on the next tick, not as loss.
//
// stop is synchronous: it blocks until the background goroutine has fully
// exited, so no Renew call can happen after stop returns (even if a tick
// fires concurrently with the call to stop).
func KeepAlive(ctx context.Context, lease Lease, id, token string, ttl time.Duration) (lost <-chan struct{}, stop func()) {
	lostCh := make(chan struct{})
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	var stopOnce sync.Once
	stopFn := func() {
		stopOnce.Do(func() { close(stopCh) })
		<-doneCh
	}

	go func() {
		defer close(doneCh)
		interval := ttl / 3
		if interval <= 0 {
			interval = time.Millisecond
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				err := lease.Renew(ctx, id, token, ttl)
				if errors.Is(err, ErrLeaseLost) {
					close(lostCh)
					return
				}
			}
		}
	}()

	return lostCh, stopFn
}
