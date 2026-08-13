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

// slowRenew wraps a real Lease but makes every Renew block until its ctx is
// cancelled (a well-behaved, ctx-respecting implementation of a call that's
// simply slow — e.g. a stalled network round-trip), signaling on started
// the moment it enters that block. Used to prove ResumeLocked cancels
// KeepAlive's context before waiting for it to stop, rather than deadlocking
// on an in-flight Renew that has nothing to unblock it.
type slowRenew struct {
	checkpointer.Lease
	started chan struct{}
}

func (l *slowRenew) Renew(ctx context.Context, id, token string, ttl time.Duration) error {
	select {
	case l.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestResumeLockedCancelsKeepAliveContextBeforeWaitingForStop(t *testing.T) {
	cp := mem.New()
	seed := gantry.NewState("go")
	if err := cp.Save(context.Background(), "run-rl-5", seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	lease := &slowRenew{Lease: mem.NewLease(), started: make(chan struct{})}
	llm := &blockingLLM{unblock: make(chan struct{}), resp: gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd}}
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))

	// A small ttl so KeepAlive's first renewal tick (ttl/3) fires quickly.
	const ttl = 15 * time.Millisecond

	resultCh := make(chan error, 1)
	go func() {
		_, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-5", ttl)
		resultCh <- err
	}()

	// Wait for a Renew call to genuinely be in flight (blocked on its ctx)
	// before letting a.Resume finish, so stop() is guaranteed to race a
	// live, currently-blocked Renew rather than one that already returned.
	select {
	case <-lease.started:
	case <-time.After(2 * time.Second):
		t.Fatal("no Renew call started within 2s")
	}

	close(llm.unblock) // let a.Resume finish; Renew is still blocked on its ctx

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("ResumeLocked: %v", err)
		}
	case <-time.After(2 * time.Second):
		// If stop() doesn't cancel runCtx before waiting for the KeepAlive
		// goroutine to exit, this call deadlocks forever: stop() blocks on
		// the goroutine returning, the goroutine blocks on ctx.Done() inside
		// Renew, and nothing left in ResumeLocked would ever cancel that ctx
		// before stop() itself returns.
		t.Fatal("ResumeLocked did not return within 2s after a.Resume finished — " +
			"stop() is likely blocked on an in-flight Renew with nothing to unblock it")
	}
}

// gatedAcquire wraps a real Lease but blocks every Acquire call until
// proceed is closed, signaling on called the moment it's entered. Used to
// simulate a whole competing resume-and-checkpoint cycle happening in the
// window before this call's Acquire has actually returned, regardless of
// whether ResumeLocked calls Acquire before or after Load.
type gatedAcquire struct {
	checkpointer.Lease
	called  chan struct{}
	proceed chan struct{}
}

func (l *gatedAcquire) Acquire(ctx context.Context, id string, ttl time.Duration) (string, error) {
	select {
	case l.called <- struct{}{}:
	default:
	}
	<-l.proceed
	return l.Lease.Acquire(ctx, id, ttl)
}

func TestResumeLockedDoesNotResumeAStaleLoadWonAfterAConcurrentSave(t *testing.T) {
	cp := mem.New()
	realLease := mem.NewLease()

	stale := gantry.NewState("go")
	stale.Iteration = 1
	if err := cp.Save(context.Background(), "run-rl-6", stale); err != nil {
		t.Fatalf("seed stale Save: %v", err)
	}

	lease := &gatedAcquire{Lease: realLease, called: make(chan struct{}), proceed: make(chan struct{})}
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	resultCh := make(chan *gantry.State, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-6", time.Minute)
		resultCh <- result
		errCh <- err
	}()

	select {
	case <-lease.called:
	case <-time.After(2 * time.Second):
		t.Fatal("Acquire was not called within 2s")
	}

	// Simulate a second worker completing an entire resume-and-checkpoint
	// cycle in the window before this call's Acquire has returned: it wins
	// the (currently unheld) lease, saves a newer checkpoint, and releases.
	tok, err := realLease.Acquire(context.Background(), "run-rl-6", time.Minute)
	if err != nil {
		t.Fatalf("simulated concurrent worker Acquire: %v", err)
	}
	fresh := gantry.NewState("go")
	fresh.Iteration = 99 // a distinctive marker no normal 1-iteration run would produce
	if err := cp.Save(context.Background(), "run-rl-6", fresh); err != nil {
		t.Fatalf("simulated concurrent worker Save: %v", err)
	}
	if err := realLease.Release(context.Background(), "run-rl-6", tok); err != nil {
		t.Fatalf("simulated concurrent worker Release: %v", err)
	}

	close(lease.proceed)

	result := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatalf("ResumeLocked: %v", err)
	}
	if result.Iteration != 99 {
		t.Fatalf("resumed from Iteration=%d, want 99 — ResumeLocked used a checkpoint it loaded before actually holding the lease, missing a newer save made by a concurrent worker in between", result.Iteration)
	}
}

// failingLoad always returns err from Load, to exercise ResumeLocked's
// release-the-lease-on-load-failure path.
type failingLoad struct {
	checkpointer.Checkpointer
	err error
}

func (c *failingLoad) Load(context.Context, string) (*gantry.State, error) { return nil, c.err }

func TestResumeLockedReleasesLeaseWhenLoadFailsAfterAcquire(t *testing.T) {
	realCP := mem.New()
	loadErr := errors.New("load boom")
	cp := &failingLoad{Checkpointer: realCP, err: loadErr}
	lease := mem.NewLease()

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	_, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-7", time.Second)
	if !errors.Is(err, loadErr) {
		t.Fatalf("ResumeLocked error = %v, want wrapped %v", err, loadErr)
	}

	// The lease must have been released despite the Load failure — a fresh
	// Acquire should succeed immediately rather than waiting out the ttl.
	if _, err := lease.Acquire(context.Background(), "run-rl-7", time.Second); err != nil {
		t.Fatalf("Acquire after failed ResumeLocked: %v (lease was not released on Load failure)", err)
	}
}

// alwaysFailRelease wraps a real Lease but forces every Release to fail
// with a non-ErrLeaseLost error, to exercise ResumeLocked's
// lease_release_failed trace-recording path (the one branch none of the
// other tests reach, since mem.Lease's own Release failures are always
// ErrLeaseLost).
type alwaysFailRelease struct {
	checkpointer.Lease
}

var errReleaseBoom = errors.New("boom")

func (l *alwaysFailRelease) Release(context.Context, string, string) error {
	return errReleaseBoom
}

func TestResumeLockedRecordsNonFatalReleaseFailureOnTrace(t *testing.T) {
	cp := mem.New()
	seed := gantry.NewState("go")
	if err := cp.Save(context.Background(), "run-rl-4", seed); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	lease := &alwaysFailRelease{Lease: mem.NewLease()}
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	final, err := checkpointer.ResumeLocked(context.Background(), a, cp, lease, "run-rl-4", time.Second)
	if err != nil {
		t.Fatalf("ResumeLocked: %v (a Release failure must not be returned as the run's error)", err)
	}

	var found *gantry.TraceEvent
	for _, ev := range final.Trace.Snapshot() {
		if ev.Name == "lease_release_failed" {
			e := ev
			found = &e
			break
		}
	}
	if found == nil {
		t.Fatal("expected a lease_release_failed trace event; got none")
	}
	if !errors.Is(found.Err, errReleaseBoom) {
		t.Errorf("trace event Err = %v, want wrapped errReleaseBoom", found.Err)
	}
	if found.Attrs["id"] != "run-rl-4" {
		t.Errorf("trace event id attr = %v, want run-rl-4", found.Attrs["id"])
	}
}
