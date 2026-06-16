package gantry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestRunSingleTurnExits(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "hello world",
		StopReason: gantry.StopReasonEnd,
	})
	a, err := gantry.NewAgent(gantry.WithLLM(mock))
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
	if state.DoneReason != gantry.DoneNoToolCalls {
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
		{Response: gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "x", Name: "noop"}}, StopReason: gantry.StopReasonToolUse}},
		{Response: gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "x", Name: "noop"}}, StopReason: gantry.StopReasonToolUse}},
		{Response: gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "x", Name: "noop"}}, StopReason: gantry.StopReasonToolUse}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneMaxIterations {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneMaxIterations)
	}
	if state.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", state.Iteration)
	}
}

func TestRunContextCancellation(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Run(ctx, "go")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestRunMiddlewareSetsDoneEarly(t *testing.T) {
	mock := eval.NewMockLLMClient() // empty script; if LLM is called we'd see an error
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	a.Use(gantry.PhaseAssembleContext, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Done = true
			s.DoneReason = gantry.DoneReason("test_early_exit")
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
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "x"})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	wantErr := errors.New("custom error")
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			return wantErr
		}
	})

	_, err := a.Run(context.Background(), "go")
	if !errors.Is(err, wantErr) {
		t.Errorf("err = %v, want %v", err, wantErr)
	}
	var tc gantry.TraceCarrier
	if !errors.As(err, &tc) {
		t.Errorf("returned error does not satisfy TraceCarrier")
	} else if tc.Trace() == nil {
		t.Errorf("Trace is nil")
	}
}
