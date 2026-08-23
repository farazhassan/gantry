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

// TestResumeOnAlreadyTerminalStateIsNoOp guards the terminal-state no-op
// contract that gantry.Agent.Resume documents for itself ("A terminal prior
// (Done == true) is returned unchanged (no-op)") but tool.Resume did not
// replicate: calling tool.Resume on a state with no PendingToolCalls at all
// (a caller mistake — e.g. calling it twice on the same already-resolved
// state) must not force a brand new LLM turn. Before the fix, the flat/nested
// loops were no-ops on such a state, and the final "nothing left pending"
// check unconditionally cleared Done/DoneReason and called agent.Resume,
// spending an extra LLM turn on an already-finished conversation.
func TestResumeOnAlreadyTerminalStateIsNoOp(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "final answer", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	state, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason == gantry.DoneClientToolCall || len(state.PendingToolCalls) != 0 {
		t.Fatalf("precondition failed: state = %#v, want a plain terminal state with no pending calls", state)
	}
	if len(mock.Requests()) != 1 {
		t.Fatalf("precondition failed: LLM called %d times, want 1", len(mock.Requests()))
	}

	got, err := tool.Resume(context.Background(), a, tool.NewRegistry(), state, []gantry.ToolResult{
		{CallID: "irrelevant", Content: "irrelevant"},
	})
	if err != nil {
		t.Fatalf("Resume on an already-terminal state: %v", err)
	}
	if got != state {
		t.Errorf("Resume returned a different *State than the one passed in; want the identical pointer back (no-op)")
	}
	if !got.Done || got.DoneReason != state.DoneReason || got.FinalOutput != state.FinalOutput {
		t.Errorf("Resume mutated the terminal state: got Done=%v DoneReason=%q FinalOutput=%q", got.Done, got.DoneReason, got.FinalOutput)
	}
	if len(mock.Requests()) != 1 {
		t.Fatalf("LLM called %d times after Resume, want still 1 (no-op must not start a new LLM turn)", len(mock.Requests()))
	}
}

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

// TestResumeUnmatchedAnswerCallIDReturnsError guards Resume's documented
// contract that "each answer's CallID must match a pending call's ID exactly
// as surfaced there": before the fix, an answer whose CallID matched nothing
// pending was silently dropped (the byID lookup for the real pending call
// simply never found it) instead of surfacing an error, leaving the caller
// with no diagnostic for their typo/stale ID.
func TestResumeUnmatchedAnswerCallIDReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}

	suspended, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	beforeMessages := len(suspended.Messages)
	beforePending := len(suspended.PendingToolCalls)

	_, err = tool.Resume(context.Background(), a, tool.NewRegistry(), suspended, []gantry.ToolResult{
		{CallID: "not-a-real-call-id", Content: "whatever"},
	})
	if err == nil {
		t.Fatal("Resume with an unmatched answer CallID: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not-a-real-call-id") {
		t.Errorf("error = %v, want it to name the offending CallID", err)
	}
	if len(suspended.Messages) != beforeMessages || len(suspended.PendingToolCalls) != beforePending {
		t.Errorf("state was mutated despite the up-front validation error: Messages %d->%d PendingToolCalls %d->%d",
			beforeMessages, len(suspended.Messages), beforePending, len(suspended.PendingToolCalls))
	}
}

// TestResumeDuplicateAnswerCallIDReturnsError is
// TestResumeUnmatchedAnswerCallIDReturnsError's sibling: two answers for the
// same CallID must not silently let the second overwrite the first (as the
// old `byID[ans.CallID] = ans` loop did) — it must be a clear error instead.
func TestResumeDuplicateAnswerCallIDReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}

	suspended, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	beforeMessages := len(suspended.Messages)
	beforePending := len(suspended.PendingToolCalls)

	_, err = tool.Resume(context.Background(), a, tool.NewRegistry(), suspended, []gantry.ToolResult{
		{CallID: "q1", Content: "first"},
		{CallID: "q1", Content: "second"},
	})
	if err == nil {
		t.Fatal("Resume with a duplicate answer CallID: want error, got nil")
	}
	if !strings.Contains(err.Error(), "q1") {
		t.Errorf("error = %v, want it to name the offending CallID", err)
	}
	if len(suspended.Messages) != beforeMessages || len(suspended.PendingToolCalls) != beforePending {
		t.Errorf("state was mutated despite the up-front validation error: Messages %d->%d PendingToolCalls %d->%d",
			beforeMessages, len(suspended.Messages), beforePending, len(suspended.PendingToolCalls))
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

// TestResumeMissingContinuationMetadataReturnsError guards against a
// regression where a nested pending call with no corresponding entry in
// state.Meta's pending-entries stash (e.g. state.Meta got out of sync with
// state.PendingToolCalls via a lossy checkpoint projection, or a
// hand-constructed state) was silently re-added to remaining with no error —
// leaving the run "suspended forever" on an unroutable call with no
// diagnostic. This is inconsistent with the other similar failure modes in
// the same loop (unknown tool, nil registry, wrong interface), which do
// return clear errors; this one now must too.
func TestResumeMissingContinuationMetadataReturnsError(t *testing.T) {
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
	pendingID := suspended.PendingToolCalls[0].ID

	// Simulate state.Meta getting out of sync with state.PendingToolCalls:
	// drop the pending-entries stash entirely, so the composite pending call
	// still surfaced in PendingToolCalls has no continuation metadata to
	// route it.
	delete(suspended.Meta, "components/tool:pending_resume")

	final, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: pendingID, Content: `{"answer":"ok"}`},
	})
	if err == nil {
		t.Fatal("Resume with corrupted/missing continuation metadata: want error, got nil")
	}
	if !strings.Contains(err.Error(), "continuation") {
		t.Errorf("error = %v, want it to identify the missing-continuation-metadata problem", err)
	}
	if len(final.PendingToolCalls) != 1 || final.PendingToolCalls[0].ID != pendingID {
		t.Fatalf("PendingToolCalls after the error = %#v, want the call still pending untouched", final.PendingToolCalls)
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

// TestResumeReSuspendWithTypedNilPendingResultBecomesToolErrorNotPanic
// guards against the same typed-nil-*gantry.PendingResult footgun as
// TestRegistryInvokeTypedNilPendingResultBecomesToolErrorNotPanic, but at
// resume.go's separate errors.As site (the nested re-suspend branch, not
// reachable through Registry.Invoke): a buggy ResumableTool.Resume doing
// `var pr *gantry.PendingResult; return out, pr` returns a non-nil error
// interface wrapping a nil pointer. errors.As still matches and would leave
// pending nil — treating that as a real suspend would panic on
// pending.Pending. This must instead fold a normal (non-panicking) tool
// error for the origin, same as any other error from Resume.
func TestResumeReSuspendWithTypedNilPendingResultBecomesToolErrorNotPanic(t *testing.T) {
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
			var nilPending *gantry.PendingResult
			return nil, nilPending
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
		t.Fatalf("re-suspended (DoneReason=%q) on a typed-nil *PendingResult from Resume; want it treated as a plain tool error", final.DoneReason)
	}
	if len(final.PendingToolCalls) != 0 {
		t.Fatalf("PendingToolCalls = %#v, want none left pending", final.PendingToolCalls)
	}

	var found bool
	for _, m := range final.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no tool_result message for origin c1 in final.Messages: %+v (transcript corruption: a tool_use with no matching tool_result)", final.Messages)
	}
}

// TestResumeNestedGroupAnsweredIncrementallyAcrossRounds guards the fix for
// the finding that a nested group with 2+ leaf calls sharing one
// ResumableTool origin (e.g. a delegate whose child asked two simultaneous
// questions in one turn — components/subagent's delegateTool.asResult sets
// Pending to the child's entire set of simultaneously-pending calls) could
// not actually be answered incrementally: before the fix, answering only one
// leaf this round discarded the ENTIRE group (including the just-supplied
// answer) back into remaining, and a later round answering the other leaf
// would have no record of the first — the caller had to re-supply every
// previously-given answer on every subsequent round. Now a partial answer is
// retained in pendingEntry.Partial across rounds, so a later round needs
// only the still-missing leaves.
func TestResumeNestedGroupAnsweredIncrementallyAcrossRounds(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "all set", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()

	var resumeCalls int
	var gotResults []gantry.ToolResult
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{
				{ID: "askA", Name: "ask_user", Input: json.RawMessage(`{"q":"a?"}`)},
				{ID: "askB", Name: "ask_user", Input: json.RawMessage(`{"q":"b?"}`)},
			},
			Resume: json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			resumeCalls++
			gotResults = results
			return json.RawMessage(`{"output":"done"}`), nil
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
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2 (one composite group of 2 leaves)", suspended.PendingToolCalls)
	}

	var idA, idB string
	for _, pc := range suspended.PendingToolCalls {
		switch {
		case strings.Contains(string(pc.Input), `"a?"`):
			idA = pc.ID
		case strings.Contains(string(pc.Input), `"b?"`):
			idB = pc.ID
		}
	}
	if idA == "" || idB == "" {
		t.Fatalf("could not identify leaf IDs: %#v", suspended.PendingToolCalls)
	}

	// Round 1: answer only leaf A. The group must stay pending, untouched by
	// rt.Resume, and A's ID must still be present so a later round can find
	// it via entry.Partial rather than losing it.
	round1, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: idA, Content: "answer A"},
	})
	if err != nil {
		t.Fatalf("Resume (round 1): %v", err)
	}
	if resumeCalls != 0 {
		t.Fatalf("specialist.Resume called %d times after round 1, want 0 (group is still incomplete)", resumeCalls)
	}
	if !round1.Done || round1.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want still suspended (B unanswered)", round1.Done, round1.DoneReason)
	}
	if len(round1.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls after round 1 = %#v, want both leaves still pending (group incomplete)", round1.PendingToolCalls)
	}

	// Round 2: answer ONLY leaf B — deliberately do not re-supply A's
	// answer. The group must now complete, and rt.Resume must see BOTH
	// answers assembled correctly.
	final, err := tool.Resume(context.Background(), a, reg, round1, []gantry.ToolResult{
		{CallID: idB, Content: "answer B"},
	})
	if err != nil {
		t.Fatalf("Resume (round 2): %v", err)
	}
	if resumeCalls != 1 {
		t.Fatalf("specialist.Resume called %d times after round 2, want exactly 1", resumeCalls)
	}
	if len(gotResults) != 2 {
		t.Fatalf("rt.Resume got %d results, want 2 (both leaves)", len(gotResults))
	}
	byCallID := map[string]gantry.ToolResult{}
	for _, r := range gotResults {
		byCallID[r.CallID] = r
	}
	if r, ok := byCallID["askA"]; !ok || r.Content != "answer A" {
		t.Errorf("results for askA = %#v (present=%v), want Content %q", r, ok, "answer A")
	}
	if r, ok := byCallID["askB"]; !ok || r.Content != "answer B" {
		t.Errorf("results for askB = %#v (present=%v), want Content %q", r, ok, "answer B")
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "all set" {
		t.Fatalf("Done=%q out=%q, want normal finish after the group completes", final.DoneReason, final.FinalOutput)
	}
}

// TestResumeNestedGroupPartialAnswerSurvivesCheckpointRoundTrip proves the
// fix survives a real JSON round-trip (simulating a checkpoint save/load)
// between the round that supplies a partial answer and the round that
// completes the group — i.e. that pendingEntry.Partial genuinely persists
// through state.Meta's JSON encoding, not merely in an in-memory
// map[string]pendingEntry that a same-process retry could still see.
func TestResumeNestedGroupPartialAnswerSurvivesCheckpointRoundTrip(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "all set", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()

	var gotResults []gantry.ToolResult
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{
				{ID: "askA", Name: "ask_user", Input: json.RawMessage(`{"q":"a?"}`)},
				{ID: "askB", Name: "ask_user", Input: json.RawMessage(`{"q":"b?"}`)},
			},
			Resume: json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			gotResults = results
			return json.RawMessage(`{"output":"done"}`), nil
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

	var idA, idB string
	for _, pc := range suspended.PendingToolCalls {
		switch {
		case strings.Contains(string(pc.Input), `"a?"`):
			idA = pc.ID
		case strings.Contains(string(pc.Input), `"b?"`):
			idB = pc.ID
		}
	}
	if idA == "" || idB == "" {
		t.Fatalf("could not identify leaf IDs: %#v", suspended.PendingToolCalls)
	}

	// Round 1: answer only A.
	round1, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: idA, Content: "answer A"},
	})
	if err != nil {
		t.Fatalf("Resume (round 1): %v", err)
	}
	if len(round1.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls after round 1 = %#v, want both leaves still pending", round1.PendingToolCalls)
	}

	// Checkpoint round-trip: marshal round1 (which now carries A's answer
	// inside pendingEntry.Partial in state.Meta) to JSON and back, exactly
	// as a real checkpointer would between suspend and the next resume.
	data, err := json.Marshal(round1)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var reloaded gantry.State
	if err := json.Unmarshal(data, &reloaded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Sanity-check that the round-trip actually went through state.Meta's
	// generic map[string]interface{} representation (proving this test
	// exercises real JSON persistence, not just the same in-memory
	// map[string]pendingEntry reused across two calls).
	raw, ok := reloaded.Meta["components/tool:pending_resume"]
	if !ok {
		t.Fatal("reloaded state.Meta missing the pending-resume stash")
	}
	if _, isTypedMap := raw.(map[string]interface{}); !isTypedMap {
		t.Fatalf("reloaded state.Meta pending-resume stash has type %T, want map[string]interface{} (i.e. a genuine JSON round-trip, not the original typed map)", raw)
	}
	blob, _ := json.Marshal(raw)
	if !strings.Contains(string(blob), `"answer A"`) {
		t.Fatalf("reloaded pending-resume stash JSON = %s, want it to contain A's partial answer", blob)
	}

	// Round 2: answer ONLY B, against the reloaded state. If Partial did not
	// survive the round-trip, this would either leave the group incomplete
	// forever or assemble it with A's answer missing.
	final, err := tool.Resume(context.Background(), a, reg, &reloaded, []gantry.ToolResult{
		{CallID: idB, Content: "answer B"},
	})
	if err != nil {
		t.Fatalf("Resume (round 2, post round-trip): %v", err)
	}
	if len(gotResults) != 2 {
		t.Fatalf("rt.Resume got %d results, want 2 (both leaves, A's recovered from the round-tripped Partial)", len(gotResults))
	}
	byCallID := map[string]gantry.ToolResult{}
	for _, r := range gotResults {
		byCallID[r.CallID] = r
	}
	if r, ok := byCallID["askA"]; !ok || r.Content != "answer A" {
		t.Errorf("results for askA = %#v (present=%v), want Content %q recovered from the round-tripped Partial", r, ok, "answer A")
	}
	if r, ok := byCallID["askB"]; !ok || r.Content != "answer B" {
		t.Errorf("results for askB = %#v (present=%v), want Content %q", r, ok, "answer B")
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "all set" {
		t.Fatalf("Done=%q out=%q, want normal finish after the group completes", final.DoneReason, final.FinalOutput)
	}
}

// TestResumeNestedGroupPartialAnswerSurvivesIntermediateValidationError
// guards the fix for the finding that a nested group's merge-into-Partial
// step only ran on the "still incomplete" path: if a group became complete
// this round (this round's byID answers plus a prior round's Partial
// together covered every leaf) but then hit one of the reg==nil/!found/!ok
// validation checks before reaching rt.Resume, this round's freshly supplied
// answer(s) were never written back into entries[origin] before commit()
// persisted it — so they were silently lost, and a later Resume call (even
// with a fixed Registry and no new answers) would find the group still
// missing exactly what was supplied in the round that errored, contradicting
// the "already-supplied answers are retained" guarantee.
//
// Round 1 answers leaf A (persists via the existing merge path). Round 2
// answers leaf B AND passes a nil Registry: the group is now complete (A
// from Partial, B from byID), but the nil-Registry check fires before
// rt.Resume is reached — B's answer must still be persisted into Partial
// despite the error. Round 3, with a valid Registry and no new answers at
// all, must now succeed, with rt.Resume seeing both A's and B's answers.
func TestResumeNestedGroupPartialAnswerSurvivesIntermediateValidationError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "all set", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	reg := tool.NewRegistry()

	var resumeCalls int
	var gotResults []gantry.ToolResult
	specialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{
				{ID: "askA", Name: "ask_user", Input: json.RawMessage(`{"q":"a?"}`)},
				{ID: "askB", Name: "ask_user", Input: json.RawMessage(`{"q":"b?"}`)},
			},
			Resume: json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			resumeCalls++
			gotResults = results
			return json.RawMessage(`{"output":"done"}`), nil
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
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2 (one composite group of 2 leaves)", suspended.PendingToolCalls)
	}

	var idA, idB string
	for _, pc := range suspended.PendingToolCalls {
		switch {
		case strings.Contains(string(pc.Input), `"a?"`):
			idA = pc.ID
		case strings.Contains(string(pc.Input), `"b?"`):
			idB = pc.ID
		}
	}
	if idA == "" || idB == "" {
		t.Fatalf("could not identify leaf IDs: %#v", suspended.PendingToolCalls)
	}

	// Round 1: answer only leaf A. Group stays incomplete, A's answer
	// persists into Partial via the existing path.
	round1, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: idA, Content: "answer A"},
	})
	if err != nil {
		t.Fatalf("Resume (round 1): %v", err)
	}
	if resumeCalls != 0 {
		t.Fatalf("specialist.Resume called %d times after round 1, want 0 (group is still incomplete)", resumeCalls)
	}
	if len(round1.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls after round 1 = %#v, want both leaves still pending (group incomplete)", round1.PendingToolCalls)
	}

	// Round 2: answer leaf B, but pass a nil Registry. The group becomes
	// complete (A from Partial, B from this round's byID), but the nil
	// Registry must produce an error before rt.Resume is reached. B's answer
	// must still be persisted into Partial despite the error.
	round2, err := tool.Resume(context.Background(), a, nil, round1, []gantry.ToolResult{
		{CallID: idB, Content: "answer B"},
	})
	if err == nil {
		t.Fatal("Resume (round 2) with nil Registry: want error, got nil")
	}
	if !strings.Contains(err.Error(), "Registry") {
		t.Errorf("Resume (round 2) error = %v, want it to identify the nil-Registry problem", err)
	}
	if resumeCalls != 0 {
		t.Fatalf("specialist.Resume called %d times after round 2, want 0 (rt.Resume must never be reached when reg is nil)", resumeCalls)
	}
	if len(round2.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls after round 2 = %#v, want both leaves still pending (errored before rt.Resume)", round2.PendingToolCalls)
	}

	// Round 3: valid Registry, no new answers supplied at all. This must now
	// succeed purely from the retained Partial (A from round 1, B from round
	// 2 despite its error), proving B's answer was not lost.
	final, err := tool.Resume(context.Background(), a, reg, round2, nil)
	if err != nil {
		t.Fatalf("Resume (round 3, no new answers): %v", err)
	}
	if resumeCalls != 1 {
		t.Fatalf("specialist.Resume called %d times after round 3, want exactly 1", resumeCalls)
	}
	if len(gotResults) != 2 {
		t.Fatalf("rt.Resume got %d results, want 2 (both leaves, B recovered from Partial despite round 2's error)", len(gotResults))
	}
	byCallID := map[string]gantry.ToolResult{}
	for _, r := range gotResults {
		byCallID[r.CallID] = r
	}
	if r, ok := byCallID["askA"]; !ok || r.Content != "answer A" {
		t.Errorf("results for askA = %#v (present=%v), want Content %q", r, ok, "answer A")
	}
	if r, ok := byCallID["askB"]; !ok || r.Content != "answer B" {
		t.Errorf("results for askB = %#v (present=%v), want Content %q recovered from Partial", r, ok, "answer B")
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "all set" {
		t.Fatalf("Done=%q out=%q, want normal finish after the group finally completes", final.DoneReason, final.FinalOutput)
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
