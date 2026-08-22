// components/tool/resume_test.go
package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func TestResumeFulfillsDeclaredClientCallAndContinues(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "Hello, Ada!", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}

	suspended, err := a.Run(context.Background(), "hi, I am Ada")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if suspended.DoneReason != gantry.DoneClientToolCall || len(suspended.PendingToolCalls) != 1 {
		t.Fatalf("not suspended: reason=%q pending=%#v", suspended.DoneReason, suspended.PendingToolCalls)
	}

	final, err := tool.Resume(context.Background(), a, tool.NewRegistry(), suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"Ada"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "Hello, Ada!" {
		t.Fatalf("resume did not finish normally: reason=%q out=%q", final.DoneReason, final.FinalOutput)
	}
}

func TestResumePartialAnswersLeaveTheRestPending(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"first?"}`)},
				{ID: "q2", Name: "ask_user", Input: json.RawMessage(`{"q":"second?"}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2", suspended.PendingToolCalls)
	}

	// Answer only q1 this round.
	partial, err := tool.Resume(context.Background(), a, tool.NewRegistry(), suspended, []gantry.ToolResult{
		{CallID: "q1", Content: "first answer"},
	})
	if err != nil {
		t.Fatalf("Resume (partial): %v", err)
	}
	if !partial.Done || partial.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want still suspended (q2 unanswered)", partial.Done, partial.DoneReason)
	}
	if len(partial.PendingToolCalls) != 1 || partial.PendingToolCalls[0].ID != "q2" {
		t.Fatalf("PendingToolCalls = %#v, want only q2 remaining", partial.PendingToolCalls)
	}
	if len(mock.Requests()) != 1 {
		t.Fatalf("LLM called %d times, want 1 (must not continue until every pending call is answered)", len(mock.Requests()))
	}

	// Now answer q2 too.
	final, err := tool.Resume(context.Background(), a, tool.NewRegistry(), partial, []gantry.ToolResult{
		{CallID: "q2", Content: "second answer"},
	})
	if err != nil {
		t.Fatalf("Resume (final): %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "done" {
		t.Fatalf("Done=%q out=%q, want normal finish", final.DoneReason, final.FinalOutput)
	}
	if len(mock.Requests()) != 2 {
		t.Fatalf("LLM called %d times, want 2 (continues only once everything is answered)", len(mock.Requests()))
	}
}
