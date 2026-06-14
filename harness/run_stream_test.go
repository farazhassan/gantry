package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// fakeToolExec pushes one synthetic result per pending tool call, mirroring the
// pattern in harness/e2e_test.go (no real tool component needed).
func fakeToolExec(a *harness.Agent) {
	a.Use(harness.PhaseToolExec, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			for _, call := range s.PendingToolCalls {
				s.ToolResults = append(s.ToolResults, harness.ToolResult{
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
		harness.LLMResponse{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "calc", Input: []byte(`{"a":2,"b":3}`)}},
			StopReason: harness.StopReasonToolUse,
		},
		harness.LLMResponse{
			Content:    "the answer is 5",
			StopReason: harness.StopReasonEnd,
		},
	)
}

func TestRunStreamNilSinkErrors(t *testing.T) {
	a, _ := harness.New(harness.WithLLM(twoTurnMock()))
	state, err := a.RunStream(context.Background(), "go", nil)
	if err == nil {
		t.Fatal("RunStream with nil sink should error")
	}
	if state == nil {
		t.Error("RunStream must return a non-nil state even on the nil-sink error")
	}
}

func TestRunStreamParityWithRun(t *testing.T) {
	a1, _ := harness.New(harness.WithLLM(twoTurnMock()))
	fakeToolExec(a1)
	runState, err := a1.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	a2, _ := harness.New(harness.WithLLM(twoTurnMock()))
	fakeToolExec(a2)
	streamState, err := a2.RunStream(context.Background(), "go", func(harness.Event) error { return nil })
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
	a, _ := harness.New(harness.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	var events []harness.Event
	_, err := a.RunStream(context.Background(), "go", func(ev harness.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected events, got none")
	}
	if events[0].Type != harness.EventPhaseStart {
		t.Errorf("first event = %q, want %q", events[0].Type, harness.EventPhaseStart)
	}

	last := events[len(events)-1]
	if last.Type != harness.EventDone {
		t.Errorf("last event = %q, want %q", last.Type, harness.EventDone)
	}
	if last.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("done reason = %q, want %q", last.DoneReason, harness.DoneNoToolCalls)
	}
	if last.FinalOutput != "the answer is 5" {
		t.Errorf("done final output = %q", last.FinalOutput)
	}

	var sawToolCall, sawToolResult bool
	for _, ev := range events {
		if ev.Type == harness.EventToolCall && ev.ToolCall != nil && ev.ToolCall.ID == "c1" {
			sawToolCall = true
		}
		if ev.Type == harness.EventToolResult && ev.ToolResult != nil && ev.ToolResult.CallID == "c1" {
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
	a, _ := harness.New(harness.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	boom := errors.New("sink boom")
	state, err := a.RunStream(context.Background(), "go", func(ev harness.Event) error {
		if ev.Type == harness.EventPhaseStart {
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
