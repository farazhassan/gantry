// components/tool/resume_test.go
package tool_test

import (
	"context"
	"encoding/json"
	"strings"
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

// TestResumeNestedLookupFailureCommitsPriorProgress guards against a
// regression where an error from an unknown tool name for one nested
// origin, encountered after another origin in the same batch already
// resolved successfully, would return before committing the successful
// origin's progress into state.PendingToolCalls/Meta. That would leave the
// already-resolved origin still listed as pending with its stale
// continuation token, so a caller who fixes the registry and retries would
// invoke that origin's ResumableTool.Resume a second time — duplicating any
// real side effect and appending a second RoleTool message for the same
// CallID.
func TestResumeNestedLookupFailureCommitsPriorProgress(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c1", Name: "specialist1", Input: json.RawMessage(`{}`)},
				{ID: "c2", Name: "specialist2", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "all done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client()); err != nil {
		t.Fatalf("install client: %v", err)
	}

	var resumeCalls int
	specialist1 := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist1", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"1?"}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			resumeCalls++
			return json.RawMessage(`{"output":"done1"}`), nil
		},
	}
	specialist2 := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist2", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask2", Name: "ask_user", Input: json.RawMessage(`{"q":"2?"}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
	}
	if err := a.With(tool.FromTools(1, specialist1, specialist2)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2", suspended.PendingToolCalls)
	}

	var c1ID, c2ID string
	for _, pc := range suspended.PendingToolCalls {
		switch {
		case strings.Contains(string(pc.Input), `"1?"`):
			c1ID = pc.ID
		case strings.Contains(string(pc.Input), `"2?"`):
			c2ID = pc.ID
		}
	}
	if c1ID == "" || c2ID == "" {
		t.Fatalf("could not identify pending call IDs: %#v", suspended.PendingToolCalls)
	}

	// reg only knows about specialist1: specialist2's origin (c2) will fail
	// lookup after c1's origin has already resolved successfully.
	reg := tool.NewRegistry()
	reg.Add(specialist1)

	final, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: c1ID, Content: `{"answer":"a1"}`},
		{CallID: c2ID, Content: `{"answer":"a2"}`},
	})
	if err == nil {
		t.Fatal("Resume: want error for unknown tool specialist2, got nil")
	}
	if resumeCalls != 1 {
		t.Fatalf("specialist1.Resume called %d times, want exactly 1", resumeCalls)
	}

	foundC1 := false
	foundC2 := false
	for _, pc := range final.PendingToolCalls {
		if pc.ID == c1ID {
			foundC1 = true
		}
		if pc.ID == c2ID {
			foundC2 = true
		}
	}
	if foundC1 {
		t.Errorf("PendingToolCalls = %#v, c1's already-resolved pending call must not still be listed", final.PendingToolCalls)
	}
	if !foundC2 {
		t.Errorf("PendingToolCalls = %#v, c2's unresolved pending call must still be listed", final.PendingToolCalls)
	}

	var c1Resolved bool
	for _, m := range final.Messages {
		if m.ToolCallID == "c1" {
			c1Resolved = true
		}
	}
	if !c1Resolved {
		t.Errorf("Messages = %#v, want a folded RoleTool message for c1 (already resolved)", final.Messages)
	}

	// Retry with a fixed registry: only the still-pending c2 needs an
	// answer. specialist1.Resume must not be invoked again.
	fullReg := tool.NewRegistry()
	fullReg.Add(specialist1)
	fullReg.Add(specialist2)

	final2, err := tool.Resume(context.Background(), a, fullReg, final, []gantry.ToolResult{
		{CallID: c2ID, Content: `{"answer":"a2"}`},
	})
	if err != nil {
		t.Fatalf("Resume (retry): %v", err)
	}
	if resumeCalls != 1 {
		t.Fatalf("specialist1.Resume called %d times after retry, want still exactly 1 (no duplicate side effect)", resumeCalls)
	}
	if final2.DoneReason != gantry.DoneNoToolCalls || final2.FinalOutput != "all done" {
		t.Fatalf("Done=%q out=%q, want normal finish after retry", final2.DoneReason, final2.FinalOutput)
	}
}

// TestResumeNilRegistryOnNestedPendingCallReturnsErrorNotPanic guards
// against a regression where Resume's doc comment says reg may be nil when
// no pending call traces back to a ResumableTool, but the nested-call
// branch unconditionally called reg.Lookup — panicking with a nil pointer
// dereference the moment a nested pending call actually showed up.
func TestResumeNilRegistryOnNestedPendingCallReturnsErrorNotPanic(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client()); err != nil {
		t.Fatalf("install client: %v", err)
	}
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"ok?"}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
	}
	if err := a.With(tool.FromTools(1, specialist)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 1 {
		t.Fatalf("PendingToolCalls = %#v, want 1", suspended.PendingToolCalls)
	}
	pendingID := suspended.PendingToolCalls[0].ID

	final, err := tool.Resume(context.Background(), a, nil, suspended, []gantry.ToolResult{
		{CallID: pendingID, Content: `{"answer":"ok"}`},
	})
	if err == nil {
		t.Fatal("Resume with nil Registry on a nested pending call: want error, got nil")
	}
	if len(final.PendingToolCalls) != 1 || final.PendingToolCalls[0].ID != pendingID {
		t.Fatalf("PendingToolCalls after nil-Registry error = %#v, want the call still pending untouched", final.PendingToolCalls)
	}
}

