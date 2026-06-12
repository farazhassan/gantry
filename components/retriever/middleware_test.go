package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithRetrieverPopulatesStateAndPrependsContext(t *testing.T) {
	docs := []harness.Document{
		{ID: "a", Content: "alpha"},
		{ID: "b", Content: "beta"},
	}
	r := retriever.NewStatic(docs)

	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.New(harness.WithLLM(mock))
	retriever.WithRetriever(a, r, 5)

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
