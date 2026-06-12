package components_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// noopTool lets tool-call turns dispatch without error so we can drive the
// max_iterations and human_aborted paths.
type noopTool struct{}

func (noopTool) Definition() harness.ToolDef {
	return harness.ToolDef{Name: "noop", Description: "noop", Schema: json.RawMessage(`{}`)}
}

func (noopTool) Invoke(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}

// TestTerminationConvention locks in, per terminal path, both Run's error
// channel (nil for resource/normal stops; sentinel for active blocks/aborts)
// and state.DoneReason. Run always returns a non-nil *State.
func TestTerminationConvention(t *testing.T) {
	t.Run("no_tool_calls_returns_nil", func(t *testing.T) {
		mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "done", StopReason: harness.StopReasonEnd})
		a, _ := harness.New(harness.WithLLM(mock))

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state == nil {
			t.Fatalf("state is nil, want non-nil *State")
		}
		if state.DoneReason != harness.DoneNoToolCalls {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneNoToolCalls)
		}
	})

	t.Run("max_iterations_returns_nil", func(t *testing.T) {
		mock := eval.NewMockLLMClient(harness.LLMResponse{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: harness.StopReasonToolUse,
		})
		a, _ := harness.New(harness.WithLLM(mock), harness.WithMaxIterations(1))
		tool.WithTool(a, noopTool{})

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state.DoneReason != harness.DoneMaxIterations {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneMaxIterations)
		}
	})

	t.Run("budget_exceeded_returns_nil", func(t *testing.T) {
		// Tool-call turn with usage over the cap: the limiter finalize check
		// terminates the loop with DoneBudgetExceeded after this iteration.
		mock := eval.NewMockLLMClient(harness.LLMResponse{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: harness.StopReasonToolUse,
			Usage:      harness.Usage{InputTokens: 1000, OutputTokens: 1000},
		})
		a, _ := harness.New(harness.WithLLM(mock))
		tool.WithTool(a, noopTool{})
		limiter.WithLimiter(a, limiter.NewBudget(limiter.Limits{MaxTokens: 1}))

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state.DoneReason != harness.DoneBudgetExceeded {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneBudgetExceeded)
		}
	})

	t.Run("guardrail_blocked_returns_sentinel", func(t *testing.T) {
		mock := eval.NewMockLLMClient() // input check fires before any LLM call
		a, _ := harness.New(harness.WithLLM(mock))
		guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)blocked`, guardrail.DirectionInput))

		state, err := a.Run(context.Background(), "this is blocked")
		if !errors.Is(err, harness.ErrGuardrailBlocked) {
			t.Fatalf("err = %v, want ErrGuardrailBlocked", err)
		}
		if state.DoneReason != harness.DoneGuardrailBlocked {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneGuardrailBlocked)
		}
	})

	t.Run("human_aborted_returns_sentinel", func(t *testing.T) {
		mock := eval.NewMockLLMClient(harness.LLMResponse{
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: harness.StopReasonToolUse,
		})
		a, _ := harness.New(harness.WithLLM(mock))
		tool.WithTool(a, noopTool{})
		humanloop.WithHumanInLoop(a, humanloop.NewAutoDenier("denied for test"))

		state, err := a.Run(context.Background(), "go")
		if !errors.Is(err, harness.ErrHumanAborted) {
			t.Fatalf("err = %v, want ErrHumanAborted", err)
		}
		if state.DoneReason != harness.DoneHumanAborted {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneHumanAborted)
		}
	})
}
