package humanloop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/eval"
)

func TestWithHumanInLoopDenialAbortsRun(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: "x", Name: "anything"}},
		StopReason: gantry.StopReasonToolUse,
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	humanloop.WithHumanInLoop(a, humanloop.NewAutoDenier("no permission"))

	state, err := a.Run(context.Background(), "go")
	if err == nil || !errors.Is(err, gantry.ErrHumanAborted) {
		t.Errorf("expected ErrHumanAborted; got %v", err)
	}
	if state.DoneReason != gantry.DoneHumanAborted {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
}

func TestWithHumanInLoopApprovalAllowsRun(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	humanloop.WithHumanInLoop(a, humanloop.NewAutoApprover())

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
}
