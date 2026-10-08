package compactor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

func TestMiddlewareFillsBudgetFromState(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithContextWindow(5000))
	preload(t, a, 3)
	setSystem(a, 400) // 100 tokens
	rc := &recordingCompactor{keep: 100}
	if err := a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 10 }})); err != nil {
		t.Fatalf("With: %v", err)
	}
	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	b := rc.budgets[0]
	if b.ContextWindow != 5000 || b.FixedTokens != 100 || b.PromptTokens != 130 {
		t.Errorf("budget = {ContextWindow:%d FixedTokens:%d PromptTokens:%d}, want {5000 100 130}",
			b.ContextWindow, b.FixedTokens, b.PromptTokens)
	}
}

func TestWrapUpHoldoutAddsToFixedTokens(t *testing.T) {
	tool := func(id string) gantry.LLMResponse {
		return gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: id, Name: "noop"}}, StopReason: gantry.StopReasonToolUse}
	}
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: tool("a")},
		{Response: tool("b")},
		{Err: overflow(1000)}, // wrap-up turn overflows
		{Response: gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(2))
	rc := &recordingCompactor{keep: 100, forceKeep: 1}
	_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No system/tools: FixedTokens is just the held-out wrap-up prompt (100).
	if got := rc.budgets[len(rc.budgets)-1].FixedTokens; got != 100 {
		t.Errorf("retry FixedTokens = %d, want 100", got)
	}
}

// anchorAt sets a provider-measured prompt size for the whole transcript. Add
// it before preload so it runs after preload has populated Messages.
func anchorAt(a *gantry.Agent, prompt int) {
	_ = a.UseNamed(gantry.PhaseAssembleContext, "anchor", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.ContextUsage = gantry.ContextUsage{PromptTokens: prompt, MessageCount: len(s.Messages)}
			return next(ctx, s)
		}
	})
}

func TestOverflowTargetIsCalibrated(t *testing.T) {
	// 4 messages × 100 + 100 fixed = 500 estimated; measured 1000 → ratio 2,
	// so System is 200 provider tokens and messages 800.
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"provider limit", overflow(500), 300},                                     // 500 − 200
		{"fallback", &gantry.ContextLengthError{Err: errors.New("too long")}, 600}, // 75% of 800
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
				{Err: tc.err},
				{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
			})
			a, _ := gantry.NewAgent(gantry.WithLLM(mock))
			anchorAt(a, 1000)
			preload(t, a, 4)
			setSystem(a, 400)
			rc := &recordingCompactor{keep: 100, forceKeep: 1}
			_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))
			if _, err := a.Run(context.Background(), ""); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != tc.want {
				t.Errorf("retry MaxTokens = %d, want %d", got, tc.want)
			}
		})
	}
}
