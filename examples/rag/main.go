package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// RunExample attaches a static retriever so documents are fetched and injected
// into the system prompt before the LLM call. It returns the terminal State so
// the test can confirm the documents were retrieved.
func RunExample(ctx context.Context) (*harness.State, error) {
	docs := []harness.Document{
		{ID: "doc-1", Content: "Gantry is a phase-based agent harness for Go."},
		{ID: "doc-2", Content: "Components attach to the agent as middleware."},
		{ID: "doc-3", Content: "The harness drives a fixed sequence of phases each turn."},
	}

	llm := eval.NewMockLLMClient(harness.LLMResponse{
		Content:    "Based on the retrieved docs, Gantry is a phase-based agent harness for Go.",
		StopReason: harness.StopReasonEnd,
	})

	a, err := harness.NewAgent(harness.WithLLM(llm))
	if err != nil {
		return nil, err
	}

	// On the first iteration the retriever fetches the top k=2 of these 3 docs,
	// stores them in state.Retrieved, and appends them to state.System. Because
	// k < len(docs) the third doc is dropped — swap in a real vector-store
	// retriever by satisfying the Retriever interface.
	retriever.WithRetriever(a, retriever.NewStatic(docs), 2)

	return a.Run(ctx, "what is gantry?")
}

func main() {
	state, err := RunExample(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("retrieved %d docs\n", len(state.Retrieved))
	fmt.Println("final output:", state.FinalOutput)
}
