package humanloop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithHumanInLoopDenialAbortsRun(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{
		ToolCalls:  []harness.ToolCall{{ID: "x", Name: "anything"}},
		StopReason: harness.StopReasonToolUse,
	})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	humanloop.WithHumanInLoop(a, humanloop.NewAutoDenier("no permission"))

	state, err := a.Run(context.Background(), "go")
	if err == nil || !errors.Is(err, harness.ErrHumanAborted) {
		t.Errorf("expected ErrHumanAborted; got %v", err)
	}
	if state.DoneReason != harness.DoneHumanAborted {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
}

func TestWithHumanInLoopApprovalAllowsRun(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "done", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))
	humanloop.WithHumanInLoop(a, humanloop.NewAutoApprover())

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneNoToolCalls)
	}
}
