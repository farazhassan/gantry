package checkpointer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/eval"
)

func TestResumeLockedRoundTrip(t *testing.T) {
	cp := mem.New()
	lease := mem.NewLease()
	seed := gantry.NewState("go")
	if err := cp.Save(context.Background(), "run-rl-1", seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	final, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-1", time.Second)
	if err != nil {
		t.Fatalf("ResumeLocked: %v", err)
	}
	if final.FinalOutput != "done" {
		t.Errorf("FinalOutput = %q, want %q", final.FinalOutput, "done")
	}

	// The lease must have been released — a fresh Acquire should succeed.
	if _, err := lease.Acquire(context.Background(), "run-rl-1", time.Second); err != nil {
		t.Fatalf("Acquire after ResumeLocked: %v (lease was not released)", err)
	}
}

func TestResumeLockedReturnsErrLeaseHeldImmediately(t *testing.T) {
	cp := mem.New()
	lease := mem.NewLease()
	seed := gantry.NewState("go")
	if err := cp.Save(context.Background(), "run-rl-2", seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	if _, err := lease.Acquire(context.Background(), "run-rl-2", time.Minute); err != nil {
		t.Fatalf("pre-acquire: %v", err)
	}

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	_, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-2", time.Minute)
	if !errors.Is(err, checkpointer.ErrLeaseHeld) {
		t.Fatalf("ResumeLocked error = %v, want ErrLeaseHeld", err)
	}
}

// blockingLLM blocks in Generate until unblock is closed, or ctx is
// cancelled — used to hold a Resume call open long enough to observe
// KeepAlive's cancel-on-lost wiring deterministically.
type blockingLLM struct {
	unblock chan struct{}
	resp    gantry.LLMResponse
}

func (b *blockingLLM) Generate(ctx context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	select {
	case <-b.unblock:
		return b.resp, nil
	case <-ctx.Done():
		return gantry.LLMResponse{}, ctx.Err()
	}
}

// alwaysLoseAfterAcquire wraps a real Lease but forces every Renew to
// report the lease lost, to deterministically exercise ResumeLocked's
// cancel-on-lost wiring without a real network race.
type alwaysLoseAfterAcquire struct {
	checkpointer.Lease
}

func (l *alwaysLoseAfterAcquire) Renew(context.Context, string, string, time.Duration) error {
	return checkpointer.ErrLeaseLost
}

func TestResumeLockedCancelsRunWhenLeaseIsLost(t *testing.T) {
	cp := mem.New()
	seed := gantry.NewState("go")
	if err := cp.Save(context.Background(), "run-rl-3", seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	lease := &alwaysLoseAfterAcquire{Lease: mem.NewLease()}
	llm := &blockingLLM{unblock: make(chan struct{})}
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))

	errCh := make(chan error, 1)
	go func() {
		_, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-3", 15*time.Millisecond)
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ResumeLocked error = %v, want context.Canceled (lease loss should cancel the in-flight Resume)", err)
		}
	case <-time.After(2 * time.Second):
		// llm.unblock is deliberately never closed. If cancellation didn't
		// actually propagate into the blocked LLM call, ResumeLocked would
		// still be waiting on it right now instead of having already
		// returned — this timeout is the failure signal for that case.
		t.Fatal("ResumeLocked did not return within 2s of the lease being lost — cancellation did not propagate into the blocked LLM call")
	}
}
