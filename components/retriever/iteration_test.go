package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
)

// TestWithRetrieverNoDuplicateSystemAcrossIterations guards against appending
// the retrieved-context block to state.System on every iteration.
// PhaseAssembleContext re-runs each iteration; state.System persists, so a
// per-iteration append would stack duplicate blocks.
func TestWithRetrieverNoDuplicateSystemAcrossIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "t1", Name: "x"}}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(5))
	if err := a.With(retriever.New(retriever.NewStatic([]gantry.Document{{ID: "d1", Content: "alpha"}}), 5)); err != nil {
		t.Fatalf("install retriever: %v", err)
	}

	if _, err := a.Run(context.Background(), "q"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	if n := strings.Count(reqs[1].System, "Retrieved context:"); n != 1 {
		t.Errorf("second LLM call System has %d retrieved blocks, want 1; System=%q", n, reqs[1].System)
	}
}
