package memory_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// TestWithMemoryNoTranscriptDuplicationAcrossIterations guards against the
// read middleware re-prepending stored history on every iteration. The
// in-run transcript already accumulates in state.Messages, so re-prepending
// would duplicate prior turns in the prompt sent to the LLM.
func TestWithMemoryNoTranscriptDuplicationAcrossIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "t1", Name: "x"}}, StopReason: harness.StopReasonToolUse},
		harness.LLMResponse{Content: "final", StopReason: harness.StopReasonEnd},
	)
	a, _ := harness.NewAgent(harness.WithLLM(mock), harness.WithMaxIterations(5))
	memory.WithMemory(a, memory.NewInMemoryStore())

	if _, err := a.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	users := 0
	for _, m := range reqs[1].Messages {
		if m.Role == harness.RoleUser && m.Content == "hello" {
			users++
		}
	}
	if users != 1 {
		t.Errorf("second LLM call saw user message %d times, want 1 (history re-prepended); messages: %+v", users, reqs[1].Messages)
	}
}
