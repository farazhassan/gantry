package components_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/humanloop"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// noopTool lets tool-call turns dispatch without error so we can drive the
// max_iterations and human_aborted paths.
type noopTool struct{}

func (noopTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "noop", Description: "noop", Schema: json.RawMessage(`{}`)}
}

func (noopTool) Invoke(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}

// TestTerminationConvention locks in, per terminal path, both Run's error
// channel (nil for resource/normal stops; sentinel for active blocks/aborts)
// and state.DoneReason. Run always returns a non-nil *State.
func TestTerminationConvention(t *testing.T) {
	t.Run("no_tool_calls_returns_nil", func(t *testing.T) {
		mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state == nil {
			t.Fatalf("state is nil, want non-nil *State")
		}
		if state.DoneReason != gantry.DoneNoToolCalls {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
		}
	})

	t.Run("max_iterations_returns_nil", func(t *testing.T) {
		mock := eval.NewMockLLMClient(gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: gantry.StopReasonToolUse,
		})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(1))
		tool.WithTool(a, noopTool{})

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state.DoneReason != gantry.DoneMaxIterations {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneMaxIterations)
		}
	})

	t.Run("budget_exceeded_returns_nil", func(t *testing.T) {
		// Tool-call turn with usage over the cap: the limiter finalize check
		// terminates the loop with DoneBudgetExceeded after this iteration.
		mock := eval.NewMockLLMClient(gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 1000, OutputTokens: 1000},
		})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))
		tool.WithTool(a, noopTool{})
		if err := a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 1}))); err != nil {
			t.Fatalf("install limiter: %v", err)
		}

		state, err := a.Run(context.Background(), "go")
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if state.DoneReason != gantry.DoneBudgetExceeded {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneBudgetExceeded)
		}
	})

	t.Run("guardrail_blocked_returns_sentinel", func(t *testing.T) {
		mock := eval.NewMockLLMClient() // input check fires before any LLM call
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))
		guardrail.WithGuardrail(a, guardrail.NewRegex(`(?i)blocked`, guardrail.DirectionInput))

		state, err := a.Run(context.Background(), "this is blocked")
		if !errors.Is(err, gantry.ErrGuardrailBlocked) {
			t.Fatalf("err = %v, want ErrGuardrailBlocked", err)
		}
		if state.DoneReason != gantry.DoneGuardrailBlocked {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneGuardrailBlocked)
		}
	})

	t.Run("human_aborted_returns_sentinel", func(t *testing.T) {
		mock := eval.NewMockLLMClient(gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "noop"}},
			StopReason: gantry.StopReasonToolUse,
		})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))
		tool.WithTool(a, noopTool{})
		humanloop.WithHumanInLoop(a, humanloop.NewAutoDenier("denied for test"))

		state, err := a.Run(context.Background(), "go")
		if !errors.Is(err, gantry.ErrHumanAborted) {
			t.Fatalf("err = %v, want ErrHumanAborted", err)
		}
		if state.DoneReason != gantry.DoneHumanAborted {
			t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneHumanAborted)
		}
	})
}
