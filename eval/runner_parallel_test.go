package eval_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRunnerParallelExecutesFaster(t *testing.T) {
	const N = 8
	dataset := make(eval.SliceDataset, N)
	for i := range dataset {
		dataset[i] = eval.Case{ID: "c", Input: "x"}
	}

	// LLM that sleeps to make sequential vs. parallel observable.
	slowLLM := &sleepLLM{d: 20 * time.Millisecond}
	factory := func(ctx context.Context) (*harness.Agent, error) {
		return harness.New(harness.WithLLM(slowLLM))
	}
	runner := eval.Runner{
		Configs:     []eval.Config{{Name: "cfg", Factory: factory}},
		Dataset:     dataset,
		Parallelism: 4,
	}
	start := time.Now()
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(start)
	if len(report.Results) != N {
		t.Fatalf("results = %d, want %d", len(report.Results), N)
	}
	// Sequential lower bound is N*20ms = 160ms. Parallel with 4 workers should
	// be well under 100ms.
	if elapsed > 120*time.Millisecond {
		t.Errorf("elapsed %v looks too sequential (parallel target < 120ms)", elapsed)
	}
}

func TestRunnerOnResultCallback(t *testing.T) {
	var hits int32
	var mu sync.Mutex
	seen := map[string]bool{}

	runner := eval.Runner{
		Configs: []eval.Config{{Name: "cfg", Factory: func(ctx context.Context) (*harness.Agent, error) {
			return harness.New(harness.WithLLM(eval.NewMockLLMClient(harness.LLMResponse{Content: "x", StopReason: harness.StopReasonEnd})))
		}}},
		Dataset: eval.SliceDataset{{ID: "c1"}, {ID: "c2"}, {ID: "c3"}},
		OnResult: func(sr eval.ScoredResult) {
			atomic.AddInt32(&hits, 1)
			mu.Lock()
			seen[sr.Case.ID] = true
			mu.Unlock()
		},
		Parallelism: 2,
	}
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if hits != 3 {
		t.Errorf("OnResult called %d times, want 3", hits)
	}
	if len(seen) != 3 {
		t.Errorf("OnResult missed cases; seen = %v", seen)
	}
}

type sleepLLM struct {
	d time.Duration
}

func (s *sleepLLM) Generate(ctx context.Context, _ harness.LLMRequest) (harness.LLMResponse, error) {
	select {
	case <-time.After(s.d):
		return harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd}, nil
	case <-ctx.Done():
		return harness.LLMResponse{}, ctx.Err()
	}
}
