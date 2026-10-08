package compactor_test

import (
	"context"
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
