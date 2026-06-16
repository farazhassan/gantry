package planner_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/planner"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// TestWithPlannerNoDuplicateSystemAcrossIterations guards against appending the
// plan block to state.System on every iteration. The inject middleware runs in
// PhaseAssembleContext, which re-runs each iteration; state.System persists, so
// a per-iteration append would stack duplicate "Plan:" blocks.
func TestWithPlannerNoDuplicateSystemAcrossIterations(t *testing.T) {
	plannerLLM := eval.NewMockLLMClient(harness.LLMResponse{Content: "1. a\n2. b"})
	mainLLM := eval.NewMockLLMClient(
		harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "t1", Name: "x"}}, StopReason: harness.StopReasonToolUse},
		harness.LLMResponse{Content: "final", StopReason: harness.StopReasonEnd},
	)
	a, _ := harness.NewAgent(harness.WithLLM(mainLLM), harness.WithMaxIterations(5))
	if err := planner.WithPlanner(a, planner.NewLLM(plannerLLM, "")); err != nil {
		t.Fatalf("WithPlanner: %v", err)
	}

	if _, err := a.Run(context.Background(), "do it"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mainLLM.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	if n := strings.Count(reqs[1].System, "Plan:"); n != 1 {
		t.Errorf("second LLM call System has %d plan blocks, want 1; System=%q", n, reqs[1].System)
	}
}
