package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestEndToEndTwoTurnWithFakeToolExec(t *testing.T) {
	mock := eval.NewMockLLMClient(
		harness.LLMResponse{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "echo", Input: []byte(`"hello"`)}},
			StopReason: harness.StopReasonToolUse,
		},
		harness.LLMResponse{
			Content:    "final answer",
			StopReason: harness.StopReasonEnd,
		},
	)
	a, _ := harness.New(harness.WithLLM(mock))

	// Fake tool exec middleware: any pending tool call → push a synthetic result.
	a.Use(harness.PhaseToolExec, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			for _, call := range s.PendingToolCalls {
				s.ToolResults = append(s.ToolResults, harness.ToolResult{
					CallID:  call.ID,
					Content: "tool-fake-result",
				})
			}
			return next(ctx, s)
		}
	})

	state, err := a.Run(context.Background(), "do it")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "final answer" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
	if state.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", state.Iteration)
	}
	if state.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneNoToolCalls)
	}

	// Verify the first LLM call saw the user input.
	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("LLM call count = %d, want 2", len(reqs))
	}
	firstReq := reqs[0]
	if len(firstReq.Messages) == 0 || firstReq.Messages[0].Role != harness.RoleUser || firstReq.Messages[0].Content != "do it" {
		t.Errorf("first LLM call did not see user input; messages: %+v", firstReq.Messages)
	}

	// Verify the second LLM call saw the tool result in the messages.
	secondReq := reqs[1]
	foundTool := false
	for _, m := range secondReq.Messages {
		if m.Role == harness.RoleTool && m.ToolCallID == "c1" && m.Content == "tool-fake-result" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("second LLM call did not see the tool result; messages: %+v", secondReq.Messages)
	}
}
