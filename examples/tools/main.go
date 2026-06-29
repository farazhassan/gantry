package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// calcTool is a trivial tool the model can invoke. A Tool is just two methods:
// Definition (advertised to the LLM) and Invoke (called when the LLM emits a
// matching tool call).
type calcTool struct{}

func (calcTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        "calc",
		Description: "adds two integers a and b",
		Schema:      json.RawMessage(`{"type":"object","properties":{"a":{"type":"integer"},"b":{"type":"integer"}},"required":["a","b"]}`),
	}
}

func (calcTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, err
	}
	return json.Marshal(args.A + args.B)
}

// RunExample gives the agent one tool and scripts a two-turn conversation:
// turn 1 the model calls calc, turn 2 it reports the result. It returns the
// terminal State for the test to inspect.
func RunExample(ctx context.Context) (*gantry.State, error) {
	llm := eval.NewMockLLMClient(
		// Turn 1: ask to run the calc tool (StopReasonToolUse keeps the loop going).
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "call-1", Name: "calc", Input: json.RawMessage(`{"a":2,"b":3}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		// Turn 2: having seen the tool result, give the final answer.
		gantry.LLMResponse{
			Content:    "2 + 3 = 5 (computed by the calc tool).",
			StopReason: gantry.StopReasonEnd,
		},
	)

	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	// Register the tool. The first argument is the dispatch parallelism;
	// 1 runs tool calls one at a time (e2e passes 4 for concurrent calls).
	if err := a.With(tool.FromTools(1, calcTool{})); err != nil {
		return nil, err
	}

	return a.Run(ctx, "what is 2 + 3?")
}

func main() {
	state, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("final output:", state.FinalOutput)
	fmt.Println("done reason: ", state.DoneReason)
}
