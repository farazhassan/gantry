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

const runID = "distributed-run-1"
const leaseTTL = 40 * time.Millisecond

// Result reports what each stage observed, so the test can assert on it.
type Result struct {
	WorkerBFirstAttemptErr error // expected: checkpointer.ErrLeaseHeld
	FinalOutput            string
	FinalIteration         int
}

// RunExample simulates worker A crashing mid-run while holding the lease,
// and worker B taking over once the lease's TTL expires.
func RunExample(ctx context.Context) (*Result, error) {
	cp := mem.New()
	lease := mem.NewLease()

	// Seed a checkpoint as if a mid-run save (checkpointer.New's
	// extraPhases, see components/checkpointer/middleware_test.go for how
	// that's produced for real) had captured it partway through a run:
	// iteration 0's user message, the assistant's tool-calling reply, and
	// the tool result are already in the transcript, but the run has not
	// reached DoneNoToolCalls yet.
	seed := &gantry.State{
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
	if err := cp.Save(ctx, runID, seed); err != nil {
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

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("worker B first attempt : %v (expected: lease held by worker A)\n", res.WorkerBFirstAttemptErr)
	fmt.Printf("worker B after TTL      : final=%q iteration=%d\n", res.FinalOutput, res.FinalIteration)
}
