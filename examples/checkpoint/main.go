package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
)

// Result bundles the live run with the state reloaded from the checkpointer, so
// the test can assert the round-trip preserved the run.
type Result struct {
	Live   *gantry.State
	Loaded *gantry.State
}

// RunExample attaches an in-memory checkpointer, runs the agent once (the
// checkpointer saves the final state during PhaseEnd), then loads the saved
// state back by id.
func RunExample(ctx context.Context) (*Result, error) {
	const runID = "run-1"

	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "The answer is 42.",
		StopReason: gantry.StopReasonEnd,
	})

	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	cp := checkpointer.NewInMemory()
	if err := a.With(checkpointer.New(cp, runID)); err != nil {
		return nil, err
	}

	live, err := a.Run(ctx, "what is the answer?")
	if err != nil {
		return nil, err
	}

	loaded, err := cp.Load(ctx, runID)
	if err != nil {
		return nil, err
	}

	return &Result{Live: live, Loaded: loaded}, nil
}

func main() {
	res, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("live   : input=%q final=%q\n", res.Live.Input, res.Live.FinalOutput)
	fmt.Printf("loaded : input=%q final=%q\n", res.Loaded.Input, res.Loaded.FinalOutput)
}
