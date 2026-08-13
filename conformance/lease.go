package conformance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// LeaseSuite verifies the contract of checkpointer.Lease. factory need not
// return a Lease over a fresh backend on each call — sub-tests use disjoint
// ids (lease-1 through lease-7, plus lease-2-other), so a single shared
// backend behind factory is safe, mirroring how CheckpointerSuite's real
// consumers (e.g. components/checkpointer/redis/store_test.go) already
// reuse one backend across every factory() call.
func LeaseSuite(t *testing.T, factory func() checkpointer.Lease) {
	t.Helper()

	t.Run("acquire_then_release_allows_reacquire", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		tok, err := l.Acquire(ctx, "lease-1", time.Minute)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := l.Release(ctx, "lease-1", tok); err != nil {
			t.Fatalf("Release: %v", err)
		}
		if _, err := l.Acquire(ctx, "lease-1", time.Minute); err != nil {
			t.Fatalf("Acquire after release: %v", err)
		}
	})

	t.Run("acquire_while_held_returns_err_lease_held", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		if _, err := l.Acquire(ctx, "lease-2", time.Minute); err != nil {
			t.Fatalf("first Acquire: %v", err)
		}
		if _, err := l.Acquire(ctx, "lease-2", time.Minute); !errors.Is(err, checkpointer.ErrLeaseHeld) {
			t.Fatalf("second Acquire: want ErrLeaseHeld, got %v", err)
		}
		// A different id must remain acquirable — held-lease rejection must
		// not be an overly-broad global lock.
		if _, err := l.Acquire(ctx, "lease-2-other", time.Minute); err != nil {
			t.Fatalf("Acquire on different id while lease-2 held: %v", err)
		}
	})

	t.Run("renew_extends_and_returns_nil", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		tok, err := l.Acquire(ctx, "lease-3", 60*time.Millisecond)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := l.Renew(ctx, "lease-3", tok, 300*time.Millisecond); err != nil {
			t.Fatalf("Renew: %v", err)
		}
		// Sleep past the ORIGINAL 60ms ttl but well inside the renewed
		// 300ms one. If Renew were a no-op, the lease would already be
		// expired here; since it isn't, this proves the renewal actually
		// extended it.
		time.Sleep(150 * time.Millisecond)
		if _, err := l.Acquire(ctx, "lease-3", time.Minute); !errors.Is(err, checkpointer.ErrLeaseHeld) {
			t.Fatalf("Acquire after Renew (past original ttl, within renewed ttl): want ErrLeaseHeld, got %v", err)
		}
	})

	t.Run("renew_with_stale_token_returns_err_lease_lost", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		if _, err := l.Acquire(ctx, "lease-4", time.Minute); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := l.Renew(ctx, "lease-4", "not-the-real-token", time.Minute); !errors.Is(err, checkpointer.ErrLeaseLost) {
			t.Fatalf("Renew with stale token: want ErrLeaseLost, got %v", err)
		}
	})

	t.Run("release_with_stale_token_returns_err_lease_lost", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		if _, err := l.Acquire(ctx, "lease-5", time.Minute); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if err := l.Release(ctx, "lease-5", "not-the-real-token"); !errors.Is(err, checkpointer.ErrLeaseLost) {
			t.Fatalf("Release with stale token: want ErrLeaseLost, got %v", err)
		}
	})

	t.Run("release_after_expiry_returns_err_lease_lost", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		tok, err := l.Acquire(ctx, "lease-7", 30*time.Millisecond)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
		// The token is still the one Acquire returned, but the lease itself
		// has expired — Release must not treat this as a valid release of a
		// still-held lease (e.g. deleting a slot another worker has since
		// reclaimed).
		if err := l.Release(ctx, "lease-7", tok); !errors.Is(err, checkpointer.ErrLeaseLost) {
			t.Fatalf("Release after expiry: want ErrLeaseLost, got %v", err)
		}
	})

	t.Run("ttl_expiry_unblocks_new_acquire", func(t *testing.T) {
		l := factory()
		ctx := context.Background()
		if _, err := l.Acquire(ctx, "lease-6", 30*time.Millisecond); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		// Establish that the lease was genuinely held before it expires, so
		// the later successful re-acquire is attributable to TTL expiry
		// specifically, not to Acquire never enforcing exclusion at all.
		if _, err := l.Acquire(ctx, "lease-6", time.Minute); !errors.Is(err, checkpointer.ErrLeaseHeld) {
			t.Fatalf("Acquire before expiry: want ErrLeaseHeld, got %v", err)
		}
		time.Sleep(150 * time.Millisecond)
		tok2, err := l.Acquire(ctx, "lease-6", time.Minute)
		if err != nil {
			t.Fatalf("Acquire after expiry: %v", err)
		}
		if tok2 == "" {
			t.Fatal("expected a non-empty token")
		}
	})
}
