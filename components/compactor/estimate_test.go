package compactor_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

func TestDefaultCountIncludesToolCalls(t *testing.T) {
	var b compactor.Budget
	plain := gantry.Message{Role: gantry.RoleAssistant, Content: "abcd"}
	withCall := gantry.Message{Role: gantry.RoleAssistant, Content: "abcd", ToolCalls: []gantry.ToolCall{
		{ID: "call_1", Name: "search", Input: json.RawMessage(`{"q":"weather in paris"}`)},
	}}
	if got := b.Count(plain); got != 1+4 {
		t.Errorf("Count(plain) = %d, want 5 (1 content + 4 overhead)", got)
	}
	if b.Count(withCall) <= b.Count(plain) {
		t.Errorf("Count(withCall) = %d, want > Count(plain) = %d", b.Count(withCall), b.Count(plain))
	}
}

func TestCustomCounterStillWins(t *testing.T) {
	b := compactor.Budget{Counter: func(gantry.Message) int { return 42 }}
	if got := b.Count(gantry.Message{Content: "x"}); got != 42 {
		t.Errorf("Count = %d, want 42", got)
	}
}

func fixedCounter(n int) compactor.Budget {
	return compactor.Budget{Counter: func(gantry.Message) int { return n }}
}

func TestEstimatePromptTokensWithAnchor(t *testing.T) {
	s := &gantry.State{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "q"},          // 0: measured
			{Role: gantry.RoleAssistant, Content: "call"},  // 1: reply, estimated
			{Role: gantry.RoleTool, Content: "result one"}, // 2: new
			{Role: gantry.RoleTool, Content: "result two"}, // 3: new
		},
		ContextUsage: gantry.ContextUsage{PromptTokens: 500, MessageCount: 1},
	}
	if got := compactor.EstimatePromptTokens(s, fixedCounter(10)); got != 530 {
		t.Errorf("EstimatePromptTokens = %d, want 530 (500 measured + 3×10)", got)
	}
}

func TestEstimatePromptTokensAnchorOnly(t *testing.T) {
	s := &gantry.State{
		Messages:     []gantry.Message{{Role: gantry.RoleUser, Content: "q"}},
		ContextUsage: gantry.ContextUsage{PromptTokens: 500, MessageCount: 1},
	}
	if got := compactor.EstimatePromptTokens(s, fixedCounter(10)); got != 500 {
		t.Errorf("EstimatePromptTokens = %d, want 500", got)
	}
}

func TestEstimatePromptTokensWithoutAnchor(t *testing.T) {
	s := &gantry.State{
		System:   "abcdefgh",                                                                       // 2 tokens
		Tools:    []gantry.ToolDef{{Name: "ab", Description: "cd", Schema: json.RawMessage(`{}`)}}, // (2+2+2+3)/4 = 2
		Messages: []gantry.Message{{Content: "a"}, {Content: "b"}},
	}
	if got := compactor.EstimatePromptTokens(s, fixedCounter(10)); got != 2+2+20 {
		t.Errorf("EstimatePromptTokens = %d, want 24", got)
	}
}

func TestEstimatePromptTokensStaleAnchorFallsBack(t *testing.T) {
	s := &gantry.State{
		Messages:     []gantry.Message{{Content: "a"}},
		ContextUsage: gantry.ContextUsage{PromptTokens: 500, MessageCount: 5}, // > len(Messages)
	}
	if got := compactor.EstimatePromptTokens(s, fixedCounter(10)); got != 10 {
		t.Errorf("EstimatePromptTokens = %d, want 10 (full estimate)", got)
	}
}

func TestSummarizingForceIgnoresSoftLimit(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "summary"})
	c := compactor.NewSummarizing(mock, 1, 1)
	msgs := []gantry.Message{
		{Role: gantry.RoleUser, Content: "a"},
		{Role: gantry.RoleAssistant, Content: "b"},
		{Role: gantry.RoleUser, Content: "c"},
	}
	// SoftLimit is far above the total, so without Force nothing happens.
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{SoftLimit: 1_000_000, Force: true})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(got) != 3 || got[1].Content != "summary" {
		t.Errorf("Compact = %+v, want head + summary + tail", got)
	}
}

func TestEstimatePromptTokensZeroMessageAnchor(t *testing.T) {
	// A measured request that sent no transcript messages (e.g. system only).
	s := &gantry.State{
		System:       "abcdefgh",
		Messages:     []gantry.Message{{Role: gantry.RoleAssistant, Content: "reply"}},
		ContextUsage: gantry.ContextUsage{PromptTokens: 300, MessageCount: 0},
	}
	if got := compactor.EstimatePromptTokens(s, fixedCounter(10)); got != 310 {
		t.Errorf("EstimatePromptTokens = %d, want 310 (300 measured + reply)", got)
	}
}
