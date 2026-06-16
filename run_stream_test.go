package gantry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// fakeToolExec pushes one synthetic result per pending tool call, mirroring the
// pattern in harness/e2e_test.go (no real tool component needed).
func fakeToolExec(a *gantry.Agent) {
	a.Use(gantry.PhaseToolExec, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			for _, call := range s.PendingToolCalls {
				s.ToolResults = append(s.ToolResults, gantry.ToolResult{
					CallID:  call.ID,
					Content: "fake:" + call.Name,
				})
			}
			return next(ctx, s)
		}
	})
}

func twoTurnMock() *eval.MockLLMClient {
	return eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "calc", Input: []byte(`{"a":2,"b":3}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{
			Content:    "the answer is 5",
			StopReason: gantry.StopReasonEnd,
		},
	)
}

func TestRunStreamNilSinkErrors(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	state, err := a.RunStream(context.Background(), "go", nil)
	if err == nil {
		t.Fatal("RunStream with nil sink should error")
	}
	if state == nil {
		t.Error("RunStream must return a non-nil state even on the nil-sink error")
	}
}

func TestRunStreamParityWithRun(t *testing.T) {
	a1, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a1)
	runState, err := a1.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	a2, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a2)
	streamState, err := a2.RunStream(context.Background(), "go", func(gantry.Event) error { return nil })
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	if runState.FinalOutput != streamState.FinalOutput {
		t.Errorf("FinalOutput: Run=%q RunStream=%q", runState.FinalOutput, streamState.FinalOutput)
	}
	if runState.DoneReason != streamState.DoneReason {
		t.Errorf("DoneReason: Run=%q RunStream=%q", runState.DoneReason, streamState.DoneReason)
	}
	if runState.Iteration != streamState.Iteration {
		t.Errorf("Iteration: Run=%d RunStream=%d", runState.Iteration, streamState.Iteration)
	}
	if len(runState.Messages) != len(streamState.Messages) {
		t.Errorf("len(Messages): Run=%d RunStream=%d", len(runState.Messages), len(streamState.Messages))
	}
}

func TestRunStreamEmitsPhaseToolDoneEvents(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	var events []gantry.Event
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	if events[0].Type != gantry.EventPhaseStart {
		t.Errorf("first event = %q, want %q", events[0].Type, gantry.EventPhaseStart)
	}

	last := events[len(events)-1]
	if last.Type != gantry.EventDone {
		t.Errorf("last event = %q, want %q", last.Type, gantry.EventDone)
	}
	if last.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("done reason = %q, want %q", last.DoneReason, gantry.DoneNoToolCalls)
	}
	if last.FinalOutput != "the answer is 5" {
		t.Errorf("done final output = %q", last.FinalOutput)
	}

	var sawToolCall, sawToolResult bool
	for _, ev := range events {
		if ev.Type == gantry.EventToolCall && ev.ToolCall != nil && ev.ToolCall.ID == "c1" {
			sawToolCall = true
		}
		if ev.Type == gantry.EventToolResult && ev.ToolResult != nil && ev.ToolResult.CallID == "c1" {
			sawToolResult = true
		}
	}
	if !sawToolCall {
		t.Error("expected a tool_call event for c1")
	}
	if !sawToolResult {
		t.Error("expected a tool_result event for c1")
	}
}

func TestRunStreamSinkErrorAborts(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	boom := errors.New("sink boom")
	state, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventPhaseStart {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want sink boom propagated", err)
	}
	if state == nil {
		t.Error("state must be non-nil even on sink error")
	}
}
