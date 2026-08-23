package gantry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// fakeToolExec pushes one synthetic result per pending tool call, mirroring the
// pattern in e2e_test.go (no real tool component needed).
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

func TestRunStreamUsageAccumulatesAndSnapshotsAreIndependent(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "calc", Input: []byte(`{"a":2,"b":3}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 10, OutputTokens: 5, Cost: 0.001},
		},
		gantry.LLMResponse{
			Content:    "the answer is 5",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 20, OutputTokens: 8, Cost: 0.002},
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	fakeToolExec(a)

	var usages []gantry.Usage // one snapshot per phase_end that has a non-nil Usage
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventPhaseEnd && ev.Usage != nil {
			usages = append(usages, *ev.Usage)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	// Usage is zero until the first PhaseLLMCall completes, then accumulates
	// across both LLM calls — 10+20 input, 5+8 output, 0.001+0.002 cost.
	if len(usages) < 2 {
		t.Fatalf("got %d phase_end usage snapshots, want at least 2", len(usages))
	}
	want := gantry.Usage{InputTokens: 30, OutputTokens: 13, Cost: 0.003}
	last := usages[len(usages)-1]
	if last != want {
		t.Errorf("final accumulated usage = %+v, want %+v", last, want)
	}

	// An earlier snapshot must not have been retroactively mutated by the
	// later accumulation — each phase_end's Usage is a copy, not a shared
	// pointer into state.Usage.
	first := usages[0]
	if first == want {
		t.Fatalf("first snapshot equals the final total — snapshots are aliased, not copied")
	}
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

	for _, ev := range events {
		if ev.Type == gantry.EventPhaseEnd && ev.Usage == nil {
			t.Errorf("phase_end %q has nil Usage, want a non-nil snapshot even when zero", ev.Phase)
		}
	}
	if last.Usage == nil {
		t.Error("done event has nil Usage, want a non-nil snapshot")
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

// TestRunStreamSuppressesToolResultEventForPendingCall proves that
// emitPhaseEffects' PhaseToolExec case skips an EventToolResult for any
// state.ToolResults entry whose Err is a *PendingResult. Such an entry has
// not actually finished — PhaseObserve's SuspendClientCalls middleware (see
// components/tool) hasn't run yet to strip it out of state.ToolResults and
// turn it into a proper suspended pending call — so reporting it here would
// falsely tell a streaming consumer the call completed (with an empty,
// non-error result, no less). A real, ordinary result in the same batch must
// still get its normal EventToolResult, proving the skip is targeted rather
// than suppressing the whole batch.
func TestRunStreamSuppressesToolResultEventForPendingCall(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	a.Use(gantry.PhaseToolExec, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			for _, call := range s.PendingToolCalls {
				s.ToolResults = append(s.ToolResults, gantry.ToolResult{
					CallID:  call.ID,
					Content: "fake:" + call.Name,
				})
			}
			// Simulate a second, still-pending call alongside the real one,
			// exactly as components/tool's dispatch middleware would leave it
			// after a Tool.Invoke returns *gantry.PendingResult.
			s.ToolResults = append(s.ToolResults, gantry.ToolResult{
				CallID: "pending1",
				Err:    &gantry.PendingResult{Pending: []gantry.ToolCall{{ID: "leaf1", Name: "ask_user"}}},
			})
			return next(ctx, s)
		}
	})

	var events []gantry.Event
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var sawRealResult, sawPendingResult bool
	for _, ev := range events {
		if ev.Type != gantry.EventToolResult || ev.ToolResult == nil {
			continue
		}
		switch ev.ToolResult.CallID {
		case "c1":
			sawRealResult = true
		case "pending1":
			sawPendingResult = true
		}
	}
	if !sawRealResult {
		t.Error("expected a tool_result event for the real, completed call c1")
	}
	if sawPendingResult {
		t.Error("got a tool_result event for the still-pending call pending1; want it suppressed")
	}
}
