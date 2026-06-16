package retriever_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// TestWithRetrieverNoDuplicateSystemAcrossIterations guards against appending
// the retrieved-context block to state.System on every iteration.
// PhaseAssembleContext re-runs each iteration; state.System persists, so a
// per-iteration append would stack duplicate blocks.
func TestWithRetrieverNoDuplicateSystemAcrossIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "t1", Name: "x"}}, StopReason: harness.StopReasonToolUse},
		harness.LLMResponse{Content: "final", StopReason: harness.StopReasonEnd},
	)
	a, _ := harness.NewAgent(harness.WithLLM(mock), harness.WithMaxIterations(5))
	retriever.WithRetriever(a, retriever.NewStatic([]harness.Document{{ID: "d1", Content: "alpha"}}), 5)

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
