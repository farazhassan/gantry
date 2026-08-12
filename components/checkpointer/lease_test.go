package checkpointer_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/farazhassan/gantry/components/checkpointer"
)

// fakeLease is a minimal, in-memory-only checkpointer.Lease double used to
// drive KeepAlive deterministically without a real backend.
type fakeLease struct {
	mu         sync.Mutex
	renewCount int
	loseAfter  int // Renew returns ErrLeaseLost starting at this call count (0 = never)
}

func (f *fakeLease) Acquire(context.Context, string, time.Duration) (string, error) {
	return "tok", nil
}

func (f *fakeLease) Renew(context.Context, string, string, time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.renewCount++
	if f.loseAfter > 0 && f.renewCount >= f.loseAfter {
		return checkpointer.ErrLeaseLost
	}
	return nil
}

func (f *fakeLease) Release(context.Context, string, string) error { return nil }

func (f *fakeLease) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renewCount
}

var _ checkpointer.Lease = (*fakeLease)(nil)

func TestKeepAliveRenewsPeriodically(t *testing.T) {
	fake := &fakeLease{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, stop := checkpointer.KeepAlive(ctx, fake, "run-1", "tok", 15*time.Millisecond)
	defer stop()

	deadline := time.After(200 * time.Millisecond)
	for fake.count() < 3 {
		select {
		case <-deadline:
			t.Fatalf("only %d renewals after 200ms, want at least 3 (ttl/3 = 5ms ticks)", fake.count())
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestKeepAliveStopsRenewingAfterStop(t *testing.T) {
	fake := &fakeLease{}
	ctx := context.Background()

	_, stop := checkpointer.KeepAlive(ctx, fake, "run-2", "tok", 15*time.Millisecond)
	time.Sleep(40 * time.Millisecond) // let a few renewals happen
	stop()
	countAtStop := fake.count()

	time.Sleep(60 * time.Millisecond) // long enough for several more ticks if it kept going
	if fake.count() != countAtStop {
		t.Fatalf("renewCount grew from %d to %d after stop()", countAtStop, fake.count())
	}
}

func TestKeepAliveClosesLostChannelOnErrLeaseLost(t *testing.T) {
	fake := &fakeLease{loseAfter: 2}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lost, stop := checkpointer.KeepAlive(ctx, fake, "run-3", "tok", 15*time.Millisecond)
	defer stop()

	select {
	case <-lost:
		// expected
	case <-time.After(500 * time.Millisecond):
		t.Fatal("lost channel was not closed within 500ms")
	}

	countAtLoss := fake.count()
	time.Sleep(60 * time.Millisecond)
	if fake.count() != countAtLoss {
		t.Fatalf("renewCount grew from %d to %d after lease was reported lost — KeepAlive should stop renewing", countAtLoss, fake.count())
	}
}
