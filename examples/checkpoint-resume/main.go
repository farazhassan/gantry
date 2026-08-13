// examples/checkpoint-resume/main.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/checkpointer/mem"
	"github.com/farazhassan/gantry/eval"
)

// leaseTTL is deliberately generous (not the tightest value that would still
// pass) — worker B's "immediate" takeover attempt in scenario 1 has to lose
// the Acquire race against worker A's still-live lease, and on a loaded CI
// runner even a few tens of milliseconds of scheduling delay between worker
// A's Acquire and worker B's could otherwise flip that outcome.
const leaseTTL = 200 * time.Millisecond

// newSeedState returns a fresh mid-run checkpoint: as if a mid-run save
// (checkpointer.New's extraPhases, see components/checkpointer/middleware_test.go
// for how that's produced for real) had captured it partway through a run —
// iteration 0's user message, the assistant's tool-calling reply, and the
// tool result are already in the transcript, but the run has not reached
// DoneNoToolCalls yet. Each caller gets its own *gantry.State: Resume
// mutates the state it's given in place, so scenarios must never share one.
func newSeedState() *gantry.State {
	return &gantry.State{
		Input: "start the task",
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "start the task"},
			{
				Role:    gantry.RoleAssistant,
				Content: "working on it",
				ToolCalls: []gantry.ToolCall{
					{ID: "c1", Name: "noop", Input: json.RawMessage("{}")},
				},
			},
			{Role: gantry.RoleTool, Content: "ok", ToolCallID: "c1"},
		},
		Iteration: 1,
		Trace:     gantry.NewTrace(),
		Meta:      map[string]any{},
	}
}

// blockingLLM blocks in Generate until unblock is closed, or ctx is
// cancelled — used to hold a Resume call open long enough to observe
// KeepAlive's or ResumeLocked's cancellation wiring deterministically,
// instead of via a real network race.
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

// Result reports what each stage of the crash-and-takeover scenario
// observed, so the test can assert on it.
type Result struct {
	WorkerBFirstAttemptErr error // expected: checkpointer.ErrLeaseHeld
	FinalOutput            string
	FinalIteration         int
}

// RunExample simulates worker A crashing mid-run while holding the lease,
// and worker B taking over once the lease's TTL expires.
func RunExample(ctx context.Context) (*Result, error) {
	const runID = "distributed-run-1"

	cp := mem.New()
	lease := mem.NewLease()

	if err := cp.Save(ctx, runID, newSeedState()); err != nil {
		return nil, fmt.Errorf("seed checkpoint: %w", err)
	}

	// --- Worker A claims the run, then "crashes": it acquires the lease
	// but never renews or releases it — exactly what happens when a
	// process dies mid-work, with no chance to run deferred cleanup.
	if _, err := lease.Acquire(ctx, runID, leaseTTL); err != nil {
		return nil, fmt.Errorf("worker A acquire: %w", err)
	}

	// --- Worker B: an immediate takeover attempt is rejected — worker A's
	// lease is still live.
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "final answer", StopReason: gantry.StopReasonEnd})
	b, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		return nil, err
	}
	if err := b.With(checkpointer.New(cp, runID, gantry.PhaseObserve)); err != nil {
		return nil, err
	}
	_, firstErr := checkpointer.ResumeLocked(ctx, b, cp, lease, runID, leaseTTL)

	// Wait out worker A's abandoned lease.
	time.Sleep(2 * leaseTTL)

	final, err := checkpointer.ResumeLocked(ctx, b, cp, lease, runID, leaseTTL)
	if err != nil {
		return nil, fmt.Errorf("worker B resume: %w", err)
	}

	return &Result{
		WorkerBFirstAttemptErr: firstErr,
		FinalOutput:            final.FinalOutput,
		FinalIteration:         final.Iteration,
	}, nil
}

// GracefulHandoffResult reports what the graceful-handoff scenario observed.
type GracefulHandoffResult struct {
	FinalOutput string
}

// RunGracefulHandoff demonstrates the non-crash path: worker A finishes its
// turn and releases the lease itself, so worker B can take over
// immediately — no TTL wait required. Contrast with RunExample, where
// worker A never releases (simulating a crash) and worker B must wait out
// the TTL before it can proceed.
func RunGracefulHandoff(ctx context.Context) (*GracefulHandoffResult, error) {
	const runID = "graceful-handoff-run"

	cp := mem.New()
	lease := mem.NewLease()

	if err := cp.Save(ctx, runID, newSeedState()); err != nil {
		return nil, fmt.Errorf("seed checkpoint: %w", err)
	}

	// Worker A claims the run, does its (here trivial) work, and — unlike
	// the crash scenario — releases the lease cleanly when it's done, e.g.
	// on a planned shutdown rather than a crash.
	token, err := lease.Acquire(ctx, runID, leaseTTL)
	if err != nil {
		return nil, fmt.Errorf("worker A acquire: %w", err)
	}
	if err := lease.Release(ctx, runID, token); err != nil {
		return nil, fmt.Errorf("worker A release: %w", err)
	}

	// Worker B can acquire right away — no TTL wait needed, since the
	// lease was actually given up rather than left to expire.
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "final answer", StopReason: gantry.StopReasonEnd})
	b, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		return nil, err
	}
	final, err := checkpointer.ResumeLocked(ctx, b, cp, lease, runID, leaseTTL)
	if err != nil {
		return nil, fmt.Errorf("worker B resume: %w", err)
	}

	return &GracefulHandoffResult{FinalOutput: final.FinalOutput}, nil
}

// LiveWorkerResult reports what the "worker B is still alive" scenario
// observed.
type LiveWorkerResult struct {
	ConcurrentAttemptErr error // expected: ErrLeaseHeld, even past the nominal TTL
	FinalOutput          string
}

// RunLiveWorkerBlocksTakeover demonstrates why ResumeLocked renews the
// lease in the background (via checkpointer.KeepAlive) instead of just
// granting a long TTL up front: worker B acquires the lease and is still
// genuinely working — its LLM call is deliberately held open here — well
// past the nominal leaseTTL. A concurrent takeover attempt by worker C is
// still rejected with ErrLeaseHeld, because KeepAlive kept renewing worker
// B's lease the whole time: a live worker is never mistaken for a crashed
// one just because its work is taking a while. Once worker B actually
// finishes and releases, worker C can take over normally.
func RunLiveWorkerBlocksTakeover(ctx context.Context) (*LiveWorkerResult, error) {
	const runID = "live-worker-run"

	cp := mem.New()
	lease := mem.NewLease()

	if err := cp.Save(ctx, runID, newSeedState()); err != nil {
		return nil, fmt.Errorf("seed checkpoint: %w", err)
	}

	unblockB := make(chan struct{})
	llmB := &blockingLLM{unblock: unblockB, resp: gantry.LLMResponse{Content: "worker B's answer", StopReason: gantry.StopReasonEnd}}
	b, err := gantry.NewAgent(gantry.WithLLM(llmB))
	if err != nil {
		return nil, err
	}

	type resumeResult struct {
		final *gantry.State
		err   error
	}
	bDone := make(chan resumeResult, 1)
	go func() {
		final, err := checkpointer.ResumeLocked(ctx, b, cp, lease, runID, leaseTTL)
		bDone <- resumeResult{final, err}
	}()

	// Give worker B time to acquire the lease and start its background
	// KeepAlive renewal before attempting the concurrent takeover.
	time.Sleep(leaseTTL / 2)

	// Wait well past several nominal TTL windows — long enough that,
	// without KeepAlive actively renewing, worker B's lease would have
	// expired multiple times over.
	time.Sleep(4 * leaseTTL)

	mockC := eval.NewMockLLMClient(gantry.LLMResponse{Content: "worker C's answer", StopReason: gantry.StopReasonEnd})
	c, err := gantry.NewAgent(gantry.WithLLM(mockC))
	if err != nil {
		return nil, err
	}
	_, concurrentErr := checkpointer.ResumeLocked(ctx, c, cp, lease, runID, leaseTTL)

	// Let worker B finish and release the lease.
	close(unblockB)
	res := <-bDone
	if res.err != nil {
		return nil, fmt.Errorf("worker B resume: %w", res.err)
	}

	return &LiveWorkerResult{
		ConcurrentAttemptErr: concurrentErr,
		FinalOutput:          res.final.FinalOutput,
	}, nil
}

// forceLoseLease wraps a real Lease but forces every Renew to report the
// lease lost, letting this scenario demonstrate ResumeLocked's
// cancel-on-lost wiring deterministically instead of via a real network
// race.
type forceLoseLease struct {
	checkpointer.Lease
}

func (l *forceLoseLease) Renew(context.Context, string, string, time.Duration) error {
	return checkpointer.ErrLeaseLost
}

// LeaseLostResult reports what the lease-loss scenario observed.
type LeaseLostResult struct {
	RunErr error // expected: context.Canceled
}

// RunLeaseLostCancelsRun demonstrates the other half of ResumeLocked's
// safety story: if a worker's lease is lost mid-run — its heartbeat fails
// to renew, e.g. a network partition, or a bug that lets another worker
// wrongly reclaim it — ResumeLocked cancels the in-flight a.Resume rather
// than letting it keep running un-owned, which could otherwise race a
// second worker taking over the same state.
func RunLeaseLostCancelsRun(ctx context.Context) (*LeaseLostResult, error) {
	const runID = "lease-lost-run"

	cp := mem.New()
	lease := &forceLoseLease{Lease: mem.NewLease()}

	if err := cp.Save(ctx, runID, newSeedState()); err != nil {
		return nil, fmt.Errorf("seed checkpoint: %w", err)
	}

	// llm.unblock is deliberately never closed: the run only returns
	// because ResumeLocked cancels it once KeepAlive reports the lease
	// lost, not because the LLM call ever completes on its own.
	llm := &blockingLLM{unblock: make(chan struct{})}
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	_, runErr := checkpointer.ResumeLocked(ctx, a, cp, lease, runID, leaseTTL)
	return &LeaseLostResult{RunErr: runErr}, nil
}

func main() {
	ctx := context.Background()

	fmt.Println("=== Scenario 1: crash, then TTL-based takeover ===")
	res, err := RunExample(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("worker B first attempt : %v (expected: lease held by worker A)\n", res.WorkerBFirstAttemptErr)
	fmt.Printf("worker B after TTL      : final=%q iteration=%d\n\n", res.FinalOutput, res.FinalIteration)

	fmt.Println("=== Scenario 2: graceful handoff, no crash ===")
	gh, err := RunGracefulHandoff(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("worker B immediate takeover : final=%q (no TTL wait needed)\n\n", gh.FinalOutput)

	fmt.Println("=== Scenario 3: live worker's heartbeat blocks a concurrent takeover ===")
	lw, err := RunLiveWorkerBlocksTakeover(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("worker C concurrent attempt : %v (expected: still lease held, despite elapsed time > TTL)\n", lw.ConcurrentAttemptErr)
	fmt.Printf("worker B eventual result    : final=%q\n\n", lw.FinalOutput)

	fmt.Println("=== Scenario 4: lease lost mid-run cancels the run ===")
	ll, err := RunLeaseLostCancelsRun(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("worker A run result : %v (expected: context.Canceled)\n", ll.RunErr)
}
