package transcript_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/transcript"
	"github.com/farazhassan/gantry/eval"
)

// TestWithTranscriptNoDuplicationAcrossIterations guards against the read
// middleware re-prepending stored history on every iteration. The in-run
// transcript already accumulates in state.Messages, so re-prepending would
// duplicate prior turns in the prompt sent to the LLM.
func TestWithTranscriptNoDuplicationAcrossIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "t1", Name: "x"}}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(5))
	if err := a.With(transcript.New(transcript.NewInMemoryStore())); err != nil {
		t.Fatalf("install transcript: %v", err)
	}

	if _, err := a.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	users := 0
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleUser && m.Content == "hello" {
			users++
		}
	}
	if users != 1 {
		t.Errorf("second LLM call saw user message %d times, want 1 (history re-prepended); messages: %+v", users, reqs[1].Messages)
	}
}
