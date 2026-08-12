package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/redis"
	"github.com/farazhassan/gantry/conformance"
)

var _ checkpointer.Lease = (*redis.Lease)(nil)

func TestLeaseConformance(t *testing.T) {
	rdb := newClient(t) // defined in store_test.go, same package
	conformance.LeaseSuite(t, func() checkpointer.Lease { return redis.NewLease(rdb) })
}

func TestLeaseKeyPrefixIsApplied(t *testing.T) {
	rdb := newClient(t)
	l := redis.NewLease(rdb, redis.WithLeaseKeyPrefix("myapp:lease:"))
	ctx := context.Background()

	if _, err := l.Acquire(ctx, "id-1", time.Minute); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if exists, err := rdb.Exists(ctx, "myapp:lease:id-1").Result(); err != nil || exists != 1 {
		t.Fatalf("expected key stored under prefix, Exists: exists=%d err=%v", exists, err)
	}
}

// TestLeaseAcquireStoresTokenAndDeadline locks in the on-the-wire
// representation of a held lease — a hash with "token" and "deadline"
// fields, not a plain string — as living documentation, so a future edit
// that regresses back to a bare SET NX (which cannot support the
// server-clock-anchored expiry check the Lease doc comment describes)
// fails a test instead of only failing conformance's timing-sensitive
// sub-test.
func TestLeaseAcquireStoresTokenAndDeadline(t *testing.T) {
	rdb := newClient(t)
	l := redis.NewLease(rdb)
	ctx := context.Background()

	tok, err := l.Acquire(ctx, "id-1", time.Minute)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	fields, err := rdb.HGetAll(ctx, "gantry:lease:id-1").Result()
	if err != nil {
		t.Fatalf("HGetAll: %v", err)
	}
	if got := fields["token"]; got != tok {
		t.Errorf("hash field token = %q, want %q", got, tok)
	}
	if fields["deadline"] == "" {
		t.Errorf("hash field deadline missing or empty: %+v", fields)
	}
}
