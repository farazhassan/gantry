package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
)

// RunBlocked runs an agent whose output trips a regex guardrail. A guardrail
// block is an *active* stop: Run sets DoneGuardrailBlocked AND returns the
// ErrGuardrailBlocked sentinel (inspect it with errors.Is). The blocked content
// is scrubbed from FinalOutput.
func RunBlocked(ctx context.Context) (*gantry.State, error) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Sure, here is something forbidden.",
		StopReason: gantry.StopReasonEnd,
	})

	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)forbidden`, guardrail.DirectionOutput))

	return a.Run(ctx, "tell me something")
}

// RunBudgetStop runs an agent whose first response blows a tiny token budget.
// A budget stop is a *resource* stop: Run sets DoneBudgetExceeded and returns a
// NIL error. That nil-vs-sentinel split is the whole lesson of this example.
func RunBudgetStop(ctx context.Context) (*gantry.State, error) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "A long, expensive answer.",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 100, OutputTokens: 50},
	})

	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	// Cap tokens far below what the response reports, so the post-call check
	// terminates the run.
	if err := a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 10}))); err != nil {
		return nil, err
	}

	return a.Run(ctx, "answer at length")
}

// RunExample runs both scenarios so a single `go run` shows the contrast.
func RunExample(ctx context.Context) error {
	blocked, blockErr := RunBlocked(ctx)
	fmt.Println("=== guardrail block ===")
	fmt.Printf("done reason: %s\n", blocked.DoneReason)
	fmt.Printf("errors.Is(err, ErrGuardrailBlocked): %v\n\n", errors.Is(blockErr, gantry.ErrGuardrailBlocked))

	budget, budgetErr := RunBudgetStop(ctx)
	fmt.Println("=== budget stop ===")
	fmt.Printf("done reason: %s\n", budget.DoneReason)
	fmt.Printf("returned error: %v\n", budgetErr)
	return nil
}

func main() {
	if err := RunExample(context.Background()); err != nil {
		log.Fatal(err)
	}
}
