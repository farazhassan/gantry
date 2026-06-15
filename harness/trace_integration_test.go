package harness_test

import (
	"context"
	"strings"
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

func TestRunNestsPhasesUnderRunSpan(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "x", StopReason: harness.StopReasonEnd})
	a, _ := harness.New(harness.WithLLM(mock))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := state.Trace.Snapshot()

	// There must be exactly one top-level "run" span, and it must be parentless.
	var runID string
	runStarts := 0
	for _, e := range events {
		if e.Kind == harness.KindSpanStart && e.Name == "run" {
			runStarts++
			runID = e.SpanID
			if e.ParentID != "" {
				t.Errorf("run span ParentID = %q, want empty (root)", e.ParentID)
			}
		}
	}
	if runStarts != 1 {
		t.Fatalf("got %d \"run\" span starts, want 1", runStarts)
	}

	// Every phase span must be parented under the run span.
	sawPhase := false
	for _, e := range events {
		if e.Kind == harness.KindSpanStart && strings.HasPrefix(e.Name, "phase:") {
			sawPhase = true
			if e.ParentID != runID {
				t.Errorf("phase span %q ParentID = %q, want run id %q", e.Name, e.ParentID, runID)
			}
		}
	}
	if !sawPhase {
		t.Fatal("expected at least one phase span")
	}
}
