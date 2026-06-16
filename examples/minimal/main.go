package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// RunExample builds the smallest possible agent — just an LLM, no tools or
// components — and runs it once. It returns the terminal State so the test can
// assert on how the loop ended.
func RunExample(ctx context.Context) (*harness.State, error) {
	// A scripted mock LLM stands in for a real provider, so the example is
	// hermetic: it compiles and runs under `go test` with no API key.
	llm := eval.NewMockLLMClient(harness.LLMResponse{
		Content:    "Hello! I'm a minimal gantry agent.",
		StopReason: harness.StopReasonEnd,
	})

	a, err := harness.NewAgent(harness.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	// One pass through the loop: assemble context -> call the LLM -> observe.
	// The response carries no tool calls, so the loop stops with
	// DoneNoToolCalls and the content becomes state.FinalOutput.
	return a.Run(ctx, "introduce yourself")
}

func main() {
	state, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("final output:", state.FinalOutput)
	fmt.Println("done reason: ", state.DoneReason)
}
