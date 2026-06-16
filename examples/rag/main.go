package main

import (
	"context"
	"fmt"
	"log"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
)

// RunExample attaches a static retriever so documents are fetched and injected
// into the system prompt before the LLM call. It returns the terminal State so
// the test can confirm the documents were retrieved.
func RunExample(ctx context.Context) (*gantry.State, error) {
	docs := []gantry.Document{
		{ID: "doc-1", Content: "Gantry is a phase-based agent framework for Go."},
		{ID: "doc-2", Content: "Components attach to the agent as middleware."},
		{ID: "doc-3", Content: "Gantry drives a fixed sequence of phases each turn."},
	}

	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Based on the retrieved docs, Gantry is a phase-based agent framework for Go.",
		StopReason: gantry.StopReasonEnd,
	})

	a, err := gantry.NewAgent(gantry.WithLLM(llm))
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
