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

func TestResumeRoutesToResumableToolAndFinishes(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "used the specialist's answer", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			if string(resume) != `{"step":1}` {
				t.Errorf("Resume got token %q, want the stashed one", resume)
			}
			if len(results) != 1 || results[0].CallID != "ask1" || results[0].Content != "the answer" {
				t.Errorf("Resume got results %#v, want the ask1 answer", results)
			}
			return json.RawMessage(`{"output":"specialist finished"}`), nil
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || len(suspended.PendingToolCalls) != 1 {
		t.Fatalf("not suspended: %#v", suspended)
	}

	final, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: "the answer"},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "used the specialist's answer" {
		t.Fatalf("Done=%q out=%q, want a normal finish", final.DoneReason, final.FinalOutput)
	}
	var gotOutput bool
	for _, m := range mock.Requests()[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" && m.Content == `{"output":"specialist finished"}` {
			gotOutput = true
		}
	}
	if !gotOutput {
		t.Error("the originating call c1's folded result must carry the ResumableTool's Resume output")
	}
}

func TestResumeRoutesToResumableToolAndReSuspends(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user"}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			if string(resume) != `{"step":1}` {
				t.Errorf("Resume got token %q, want the stashed one", resume)
			}
			if len(results) != 1 || results[0].CallID != "ask1" || results[0].Content != "first answer" {
				t.Errorf("Resume got results %#v, want the ask1 answer", results)
			}
			return nil, &gantry.PendingResult{
				Pending: []gantry.ToolCall{{ID: "ask2", Name: "ask_user"}},
				Resume:  json.RawMessage(`{"step":2}`),
			}
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	firstID := suspended.PendingToolCalls[0].ID

	again, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: firstID, Content: "first answer"},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if !again.Done || again.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want re-suspended", again.Done, again.DoneReason)
	}
	if len(again.PendingToolCalls) != 1 || again.PendingToolCalls[0].Name != "ask_user" {
		t.Fatalf("PendingToolCalls = %#v, want the new ask2 leaf", again.PendingToolCalls)
	}
	if again.PendingToolCalls[0].ID == firstID {
		t.Error("the re-suspended call must get a fresh composite ID, not reuse the first round's")
	}
	if len(mock.Requests()) != 1 {
		t.Errorf("LLM called %d times, want 1 (still suspended, must not continue)", len(mock.Requests()))
	}
}

// TestResumeReSuspendWithEmptyPendingResultBecomesToolError proves the
// re-suspend-via-Resume half of the whole-branch-review fix: a
// ResumableTool.Resume call that itself returns a *gantry.PendingResult
// with Pending nil is a contract violation, just like an empty-Pending
// result from a fresh Invoke — "suspend with nothing to wait for" is a
// contradiction. resume.go's nested-call loop builds this case directly
// (it never goes through SuspendClientCalls, unlike a fresh Invoke's
// result), so it needs its own check: without it, origin c1 would get no
// tool_result message at all, and a stale pending-resume entry for it would
// linger in state.Meta even though nothing is left pending.
func TestResumeReSuspendWithEmptyPendingResultBecomesToolError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user"}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			// Contract violation: pending again, but with nothing to wait
			// on.
			return nil, &gantry.PendingResult{Resume: json.RawMessage(`{"step":2}`)}
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	firstID := suspended.PendingToolCalls[0].ID

	final, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: firstID, Content: "first answer"},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.Done && final.DoneReason == gantry.DoneClientToolCall {
		t.Fatalf("re-suspended (DoneReason=%q) on an empty-Pending PendingResult from Resume; want it treated as a plain tool error", final.DoneReason)
	}
	if len(final.PendingToolCalls) != 0 {
		t.Fatalf("PendingToolCalls = %#v, want none left pending", final.PendingToolCalls)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "done" {
		t.Fatalf("Done=%v DoneReason=%q FinalOutput=%q, want a normal finish after a second LLM turn", final.Done, final.DoneReason, final.FinalOutput)
	}
	if _, ok := final.Meta["components/tool:pending_resume"]; ok {
		t.Error("state.Meta still has a stale components/tool:pending_resume entry for origin c1, which is no longer pending")
	}

	var found bool
	for _, m := range final.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			found = true
			if !strings.Contains(m.Content, "specialist") || !strings.Contains(m.Content, "PendingResult") {
				t.Errorf("tool_result content for c1 = %q, want it to identify the tool and the contract violation", m.Content)
			}
		}
	}
	if !found {
		t.Fatalf("no tool_result message for origin c1 in final.Messages: %+v (transcript corruption: a tool_use with no matching tool_result)", final.Messages)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("LLM requests = %d, want 2 (Resume must continue to a second LLM turn once nothing is left pending)", len(reqs))
	}
	found = false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("second LLM request has no tool_result message for c1; messages: %+v (transcript corruption reaching the LLM)", reqs[1].Messages)
	}
}

func TestResumeSurvivesSimulatedCheckpointRoundTrip(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user"}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			return json.RawMessage(`{"output":"ok"}`), nil
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Simulate a checkpoint save/load between suspend and resume.
	data, err := json.Marshal(suspended)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded gantry.State
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	final, err := tool.Resume(context.Background(), a, reg, &reloaded, []gantry.ToolResult{
		{CallID: reloaded.PendingToolCalls[0].ID, Content: "the answer"},
	})
	if err != nil {
		t.Fatalf("Resume after round-trip: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "done" {
		t.Fatalf("Done=%q out=%q, want a normal finish after a checkpoint round-trip", final.DoneReason, final.FinalOutput)
	}
}

// TestResumeSurvivesCheckpointRoundTripThroughReSuspend guards against a
// regression where the map[string]pendingEntry that Resume writes back into
// state.Meta on the re-suspend branch (via setPendingEntries) fails to
// survive a second real JSON round-trip. A real checkpointer persists state
// after every suspend, not just the first, so the freshly-constructed
// pending entries produced mid-Resume-call must marshal/unmarshal just as
// cleanly as the entries the SuspendClientCalls middleware originally wrote.
func TestResumeSurvivesCheckpointRoundTripThroughReSuspend(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()

	var resumeCalls int
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user"}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			resumeCalls++
			if resumeCalls == 1 {
				return nil, &gantry.PendingResult{
					Pending: []gantry.ToolCall{{ID: "ask2", Name: "ask_user"}},
					Resume:  json.RawMessage(`{"step":2}`),
				}
			}
			return json.RawMessage(`{"output":"ok"}`), nil
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 1 {
		t.Fatalf("PendingToolCalls = %#v, want 1", suspended.PendingToolCalls)
	}
	firstID := suspended.PendingToolCalls[0].ID

	// First checkpoint round-trip: suspend -> JSON marshal/unmarshal.
	data, err := json.Marshal(suspended)
	if err != nil {
		t.Fatalf("marshal (1st): %v", err)
	}
	var reloaded gantry.State
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("unmarshal (1st): %v", err)
	}

	again, err := tool.Resume(context.Background(), a, reg, &reloaded, []gantry.ToolResult{
		{CallID: firstID, Content: "first answer"},
	})
	if err != nil {
		t.Fatalf("Resume (1st): %v", err)
	}
	if !again.Done || again.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want re-suspended", again.Done, again.DoneReason)
	}
	if len(again.PendingToolCalls) != 1 || again.PendingToolCalls[0].Name != "ask_user" {
		t.Fatalf("PendingToolCalls = %#v, want the new ask2 leaf", again.PendingToolCalls)
	}
	if again.PendingToolCalls[0].ID == firstID {
		t.Error("the re-suspended call must get a fresh composite ID, not reuse the first round's")
	}
	secondID := again.PendingToolCalls[0].ID

	// Second checkpoint round-trip: the re-suspended state (with the
	// map[string]pendingEntry that Resume wrote back into Meta mid-call)
	// must itself survive a real JSON marshal/unmarshal.
	data2, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal (2nd): %v", err)
	}
	var reloaded2 gantry.State
	if err := json.Unmarshal(data2, &reloaded2); err != nil {
		t.Fatalf("unmarshal (2nd): %v", err)
	}

	final, err := tool.Resume(context.Background(), a, reg, &reloaded2, []gantry.ToolResult{
		{CallID: secondID, Content: "second answer"},
	})
	if err != nil {
		t.Fatalf("Resume (2nd): %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "done" {
		t.Fatalf("Done=%q out=%q, want a normal finish after the second checkpoint round-trip", final.DoneReason, final.FinalOutput)
	}
	if resumeCalls != 2 {
		t.Fatalf("specialist.Resume called %d times, want exactly 2", resumeCalls)
	}
}
