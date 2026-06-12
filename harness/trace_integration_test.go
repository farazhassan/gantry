package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRunRecordsPhaseSpans(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "x", StopReason: harness.StopReasonEnd})
	a, _ := harness.New(harness.WithLLM(mock))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := state.Trace.Snapshot()
	spanStarts := map[string]int{}
	spanEnds := map[string]int{}
	for _, e := range events {
		switch e.Kind {
		case harness.KindSpanStart:
			spanStarts[e.Name]++
		case harness.KindSpanEnd:
			spanEnds[e.Name]++
		}
	}

	// With StopReasonEnd (no tool calls), post_llm marks the run done, so tool_exec
	// and observe are not executed. We verify the phases that do run record spans.
	expect := []string{
		"phase:start",
		"phase:assemble_context",
		"phase:llm_call",
		"phase:post_llm",
		"phase:end",
	}
	for _, name := range expect {
		if spanStarts[name] != 1 {
			t.Errorf("span %q starts = %d, want 1", name, spanStarts[name])
		}
		if spanEnds[name] != 1 {
			t.Errorf("span %q ends = %d, want 1", name, spanEnds[name])
		}
	}

	// Skipped phases must have no spans.
	for _, name := range []string{"phase:tool_exec", "phase:observe"} {
		if spanStarts[name] != 0 {
			t.Errorf("span %q starts = %d, want 0 (phase should be skipped)", name, spanStarts[name])
		}
		if spanEnds[name] != 0 {
			t.Errorf("span %q ends = %d, want 0 (phase should be skipped)", name, spanEnds[name])
		}
	}
}
