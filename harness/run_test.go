package harness_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRunSingleTurnExits(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{
		Content:    "hello world",
		StopReason: harness.StopReasonEnd,
	})
	a, err := harness.NewAgent(harness.WithLLM(mock))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	state, err := a.Run(context.Background(), "say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done {
		t.Errorf("expected Done=true")
	}
	if state.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
	if state.FinalOutput != "hello world" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
	if state.Iteration != 1 {
		t.Errorf("Iteration = %d, want 1", state.Iteration)
	}
}

func TestRunMaxIterationsTerminates(t *testing.T) {
	// LLM keeps requesting a tool that's never executed (no WithTool registered).
	// The loop should hit MaxIterations.
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "x", Name: "noop"}}, StopReason: harness.StopReasonToolUse}},
		{Response: harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "x", Name: "noop"}}, StopReason: harness.StopReasonToolUse}},
		{Response: harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "x", Name: "noop"}}, StopReason: harness.StopReasonToolUse}},
	})
	a, _ := harness.NewAgent(harness.WithLLM(mock), harness.WithMaxIterations(2))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != harness.DoneMaxIterations {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneMaxIterations)
	}
	if state.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", state.Iteration)
	}
}

func TestRunContextCancellation(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "x", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Run(ctx, "go")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRunMiddlewareSetsDoneEarly(t *testing.T) {
	mock := eval.NewMockLLMClient() // empty script; if LLM is called we'd see an error
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	a.Use(harness.PhaseAssembleContext, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			s.Done = true
			s.DoneReason = harness.DoneReason("test_early_exit")
			s.FinalOutput = "skipped"
			return next(ctx, s)
		}
	})

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "skipped" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("LLM should not have been called; got %d requests", len(mock.Requests()))
	}
}

func TestRunMiddlewareErrorBubblesWithTrace(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "x"})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	wantErr := errors.New("custom error")
	a.Use(harness.PhaseLLMCall, func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			return wantErr
		}
	})

	_, err := a.Run(context.Background(), "go")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	var tc harness.TraceCarrier
	if !errors.As(err, &tc) {
		t.Errorf("returned error does not satisfy TraceCarrier")
	} else if tc.Trace() == nil {
		t.Errorf("Trace is nil")
	}
}
