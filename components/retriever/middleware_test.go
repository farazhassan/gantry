package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
)

func TestWithRetrieverPopulatesStateAndPrependsContext(t *testing.T) {
	docs := []gantry.Document{
		{ID: "a", Content: "alpha"},
		{ID: "b", Content: "beta"},
	}
	r := retriever.NewStatic(docs)

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(retriever.New(r, 5)); err != nil {
		t.Fatalf("install retriever: %v", err)
	}

	state, err := a.Run(context.Background(), "search query")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(state.Retrieved) != 2 {
		t.Errorf("Retrieved = %+v", state.Retrieved)
	}
	req := mock.Requests()[0]
	if !strings.Contains(req.System, "alpha") || !strings.Contains(req.System, "beta") {
		t.Errorf("retrieved docs not folded into system prompt: %q", req.System)
	}
}
