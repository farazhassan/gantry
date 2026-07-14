package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/subagent"
	"github.com/farazhassan/gantry/eval"
)

// RunExample builds a coordinator with two specialist sub-agents and asks it
// for a short post about Go's context package. Turn 1 the coordinator
// delegates fact-finding to the researcher; turn 2 it hands the findings to
// the writer via the context field; turn 3 it returns the writer's draft. It
// returns the terminal State for the test to inspect.
func RunExample(ctx context.Context) (*gantry.State, error) {
	researcherLLM := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "context.Context carries deadlines, cancellation signals, and request-scoped values across API boundaries.",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 100, OutputTokens: 40},
	})
	researcher, err := gantry.NewAgent(gantry.WithLLM(researcherLLM))
	if err != nil {
		return nil, err
	}

	writerLLM := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Go's context package is the plumbing of cancellation: one small interface that threads deadlines and signals through every layer of a program.",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 80, OutputTokens: 60},
	})
	writer, err := gantry.NewAgent(gantry.WithLLM(writerLLM))
	if err != nil {
		return nil, err
	}

	coordinatorLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{{
				ID:    "call-1",
				Name:  "researcher",
				Input: json.RawMessage(`{"goal":"gather key facts about Go's context package"}`),
			}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 10, OutputTokens: 5},
		},
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{{
				ID:    "call-2",
				Name:  "writer",
				Input: json.RawMessage(`{"goal":"write a one-sentence post about Go's context package","context":"context.Context carries deadlines, cancellation signals, and request-scoped values across API boundaries."}`),
			}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 12, OutputTokens: 6},
		},
		gantry.LLMResponse{
			Content:    "Here is the post: Go's context package is the plumbing of cancellation: one small interface that threads deadlines and signals through every layer of a program.",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 14, OutputTokens: 7},
		},
	)
	coordinator, err := gantry.NewAgent(
		gantry.WithLLM(coordinatorLLM),
		gantry.WithComponents(subagent.Component(1,
			subagent.New("researcher", "Delegates fact-finding to a research specialist. Give it a focused goal.", researcher),
			subagent.New("writer", "Delegates prose to a writing specialist. Pass findings in the context field.", writer),
		)),
	)
	if err != nil {
		return nil, err
	}

	return coordinator.Run(ctx, "write a short post about Go's context package")
}

func main() {
	state, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("final output:", state.FinalOutput)
	fmt.Println("done reason: ", state.DoneReason)
	fmt.Printf("usage (incl. sub-agents): %d in / %d out\n", state.Usage.InputTokens, state.Usage.OutputTokens)
}
