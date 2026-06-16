package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry/harness"
)

// flakyLLM is a hand-written LLMClient — the whole interface is one method,
// Generate. It fails the first failFirst calls (simulating a transient error)
// and succeeds afterward, giving the retry middleware something to recover from.
type flakyLLM struct {
	failFirst int
	calls     int
}

func (f *flakyLLM) Generate(_ context.Context, _ harness.LLMRequest) (harness.LLMResponse, error) {
	f.calls++
	if f.calls <= f.failFirst {
		return harness.LLMResponse{}, fmt.Errorf("%w: simulated transient failure (call %d)", harness.ErrLLMTransient, f.calls)
	}
	return harness.LLMResponse{
		Content:    "Succeeded after a retry.",
		StopReason: harness.StopReasonEnd,
	}, nil
}

// Result bundles the terminal state with what the middleware observed, so the
// test can prove the retry actually happened.
type Result struct {
	State    *harness.State
	Attempts int      // how many times the LLM was invoked
	Logged   []string // one entry per LLM attempt, recorded by the logging middleware
}

// RunExample wires two middleware onto PhaseLLMCall to demonstrate the onion
// model: a logging middleware that records each call, and a retry middleware
// that re-invokes the inner chain on error.
func RunExample(ctx context.Context) (*Result, error) {
	llm := &flakyLLM{failFirst: 1}

	a, err := harness.NewAgent(harness.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	var logged []string

	// Registration order is innermost-first (see harness.Compose): the chain
	// [logging, retry] composes to retry(logging(inner)). retry is the outer
	// layer, so each retry re-enters logging and the inner LLM call — that is
	// why Logged gets one entry per attempt.
	if err := a.UseNamed(harness.PhaseLLMCall, "logging", func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			logged = append(logged, "llm_call")
			return next(ctx, s)
		}
	}); err != nil {
		return nil, err
	}

	if err := a.UseNamed(harness.PhaseLLMCall, "retry", func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			const maxAttempts = 3
			var lastErr error
			for attempt := 1; attempt <= maxAttempts; attempt++ {
				lastErr = next(ctx, s)
				if lastErr == nil {
					return nil
				}
			}
			return lastErr
		}
	}); err != nil {
		return nil, err
	}

	state, err := a.Run(ctx, "say hello")
	if err != nil {
		return nil, err
	}
	return &Result{State: state, Attempts: llm.calls, Logged: logged}, nil
}

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("llm attempts: %d\n", res.Attempts)
	fmt.Printf("logged calls: %d\n", len(res.Logged))
	fmt.Println("final output:", res.State.FinalOutput)
}
