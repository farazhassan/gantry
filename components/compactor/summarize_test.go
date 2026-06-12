package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestSummarizingCompactorReplacesMiddleWithSummary(t *testing.T) {
	// The mock LLM returns a fixed summary regardless of input.
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "SUMMARY", StopReason: harness.StopReasonEnd})
	c := compactor.NewSummarizing(mock, 1, 1)

	msgs := []harness.Message{
		{Role: harness.RoleSystem, Content: "head"},
		{Role: harness.RoleUser, Content: "mid1"},
		{Role: harness.RoleAssistant, Content: "mid2"},
		{Role: harness.RoleUser, Content: "tail"},
	}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{MaxTokens: 1, SoftLimit: 1})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	// Expect: head, system(SUMMARY), tail
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3; got %+v", len(got), got)
	}
	if got[1].Content != "SUMMARY" {
		t.Errorf("middle should be summary; got %q", got[1].Content)
	}
}

func TestNewSummarizingValidatesArgs(t *testing.T) {
	tests := []struct {
		name       string
		head, tail int
		wantPanic  bool
	}{
		{"negative head", -1, 2, true},
		{"negative tail", 2, -1, true},
		{"both negative", -1, -1, true},
		{"both zero summarizes all", 0, 0, false},
		{"head zero tail positive", 0, 2, false},
		{"both positive", 1, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tt.wantPanic && r == nil {
					t.Errorf("NewSummarizing(_, %d, %d): expected panic, got none", tt.head, tt.tail)
				}
				if !tt.wantPanic && r != nil {
					t.Errorf("NewSummarizing(_, %d, %d): unexpected panic: %v", tt.head, tt.tail, r)
				}
			}()
			_ = compactor.NewSummarizing(eval.NewMockLLMClient(), tt.head, tt.tail)
		})
	}
}

func TestSummarizingCompactorPassesThroughWhenSmall(t *testing.T) {
	mock := eval.NewMockLLMClient()
	c := compactor.NewSummarizing(mock, 1, 1)
	msgs := []harness.Message{{Content: "a"}, {Content: "b"}}
	got, _ := c.Compact(context.Background(), msgs, compactor.Budget{MaxTokens: 100})
	if len(got) != 2 {
		t.Errorf("len = %d, want 2", len(got))
	}
	if len(mock.Requests()) != 0 {
		t.Errorf("mock LLM should not have been called; got %d requests", len(mock.Requests()))
	}
}

func TestSummarizingPassThroughReturnsIndependentCopy(t *testing.T) {
	// head+tail = 4 >= len(msgs) = 3, so Compact takes the pass-through path
	// and never calls the LLM client.
	c := compactor.NewSummarizing(eval.NewMockLLMClient(), 2, 2)
	msgs := []harness.Message{{Content: "a"}, {Content: "b"}, {Content: "c"}}
	got, err := c.Compact(context.Background(), msgs, compactor.Budget{})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (pass-through)", len(got))
	}
	got[0].Content = "MUTATED"
	if msgs[0].Content != "a" {
		t.Errorf("pass-through aliased input: msgs[0] = %q, want \"a\"", msgs[0].Content)
	}
}
