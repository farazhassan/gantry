package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func askDef() gantry.ToolDef {
	return gantry.ToolDef{Name: "ask_user", Description: "ask the human", Schema: json.RawMessage(`{}`)}
}

func TestClientOnlyTurnSuspends(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want true / %q", state.Done, state.DoneReason, gantry.DoneClientToolCall)
	}
	if len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q1" {
		t.Fatalf("PendingToolCalls = %#v, want the ask_user call", state.PendingToolCalls)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "ask_user" {
		t.Fatalf("ask_user not advertised: %#v", reqs)
	}
}

func TestMixedTurnRunsServerToolsAndSuspends(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "s1", Name: "add_one", Input: json.RawMessage(`5`)},
				{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"ok?"}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v reason=%q, want suspend", state.Done, state.DoneReason)
	}
	if len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q1" {
		t.Fatalf("PendingToolCalls = %#v, want only q1", state.PendingToolCalls)
	}
	var sawServerResult bool
	for _, m := range state.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "s1" && m.Content == "6" {
			sawServerResult = true
		}
		if m.Role == gantry.RoleTool && m.ToolCallID == "q1" {
			t.Fatalf("client call q1 must not have a tool result")
		}
	}
	if !sawServerResult {
		t.Fatalf("server tool result for s1 missing; messages: %#v", state.Messages)
	}
}

func TestNoClientToolsLeavesLoopUnchanged(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "s1", Name: "add_one", Input: json.RawMessage(`5`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls || state.FinalOutput != "final" {
		t.Fatalf("Done=%q out=%q, want normal finish", state.DoneReason, state.FinalOutput)
	}
}

func TestResumeDoesNotDuplicateAdvertisedTools(t *testing.T) {
	// Reusing the same *State across Run -> (clear terminal fields) -> Resume
	// must re-run PhaseStart cleanly, not accumulate duplicate ToolDefs.
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	suspended, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Fulfill the client call and clear terminal fields, then resume in place.
	suspended.Messages = append(suspended.Messages, gantry.Message{
		Role:       gantry.RoleTool,
		ToolCallID: suspended.PendingToolCalls[0].ID,
		Content:    `{"answer":"Ada"}`,
	})
	suspended.Done = false
	suspended.DoneReason = ""
	suspended.PendingToolCalls = nil

	if _, err := a.Resume(context.Background(), suspended); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d LLM requests, want 2 (run + resume)", len(reqs))
	}
	if len(reqs[1].Tools) != 1 || reqs[1].Tools[0].Name != "ask_user" {
		t.Fatalf("resume request advertised %#v, want exactly one ask_user (no duplicates)", reqs[1].Tools)
	}
}

func TestClientToolNameCollisionPanics(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "x", Name: "add_one", Input: json.RawMessage(`1`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}
	if err := a.With(tool.Client(gantry.ToolDef{Name: "add_one", Description: "collide", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools: %v", err)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on client/registered tool name collision")
		}
	}()
	_, _ = a.Run(context.Background(), "go")
}

func TestClientDoubleInstallReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.Client(askDef())); err == nil {
		t.Fatal("second install: want error, got nil")
	}
}

func TestClientEmptyNameReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	emptyDef := gantry.ToolDef{Name: "", Description: "bad", Schema: json.RawMessage(`{}`)}
	if err := a.With(tool.Client(emptyDef)); err == nil {
		t.Fatal("empty tool name: want error, got nil")
	}
}

func TestClientDuplicateNameReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	dupDef := gantry.ToolDef{Name: "same", Description: "dup", Schema: json.RawMessage(`{}`)}
	if err := a.With(tool.Client(dupDef, dupDef)); err == nil {
		t.Fatal("duplicate tool name: want error, got nil")
	}
}

func TestDynamicClientSuspendsOnPerRunTool(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}

	prior := &gantry.State{}
	if err := tool.SetPendingClientTools(prior, gantry.ToolDef{
		Name:        "get_location",
		Description: "returns the browser's location",
		Schema:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("SetPendingClientTools: %v", err)
	}

	state, err := a.RunFrom(context.Background(), prior, "where am I?")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want true / %q", state.Done, state.DoneReason, gantry.DoneClientToolCall)
	}
	if len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "q1" {
		t.Fatalf("PendingToolCalls = %#v, want the get_location call", state.PendingToolCalls)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "get_location" {
		t.Fatalf("get_location not advertised: %#v", reqs)
	}
}

func TestDynamicClientDoesNotPersistAcrossCalls(t *testing.T) {
	// SetPendingClientTools must be called again for every run — nothing
	// should silently carry the tool forward once state.Tools is rebuilt.
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "no tools here", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}

	state, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 0 {
		t.Fatalf("Tools = %#v, want none advertised when SetPendingClientTools was never called", reqs[0].Tools)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Fatalf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
}

func TestDynamicClientDoesNotReadvertiseOnLaterResumeWithoutResetting(t *testing.T) {
	// SetPendingClientTools's contract is "the very next run/resume call on
	// s — and no further". Calling it once, suspending, fulfilling the call,
	// and then Resuming the SAME *State without calling it again must NOT
	// re-advertise the tool on that second call.
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}

	prior := &gantry.State{}
	if err := tool.SetPendingClientTools(prior, gantry.ToolDef{
		Name:        "get_location",
		Description: "returns the browser's location",
		Schema:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("SetPendingClientTools: %v", err)
	}

	suspended, err := a.RunFrom(context.Background(), prior, "where am I?")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend", suspended.Done, suspended.DoneReason)
	}

	// Fulfill the client call and clear terminal fields, then resume the
	// SAME state WITHOUT calling SetPendingClientTools again.
	suspended.Messages = append(suspended.Messages, gantry.Message{
		Role:       gantry.RoleTool,
		ToolCallID: suspended.PendingToolCalls[0].ID,
		Content:    `{"lat":0,"lng":0}`,
	})
	suspended.Done = false
	suspended.DoneReason = ""
	suspended.PendingToolCalls = nil

	if _, err := a.Resume(context.Background(), suspended); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("got %d LLM requests, want 2 (run + resume)", len(reqs))
	}
	if len(reqs[1].Tools) != 0 {
		t.Fatalf("resume request advertised %#v, want none (SetPendingClientTools was not called again)", reqs[1].Tools)
	}
}

func TestSuspendClientCallsInstalledReportsBothPaths(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})

	bare, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if tool.SuspendClientCallsInstalled(bare) {
		t.Fatal("bare agent: want false")
	}

	withClient, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := withClient.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}
	if !tool.SuspendClientCallsInstalled(withClient) {
		t.Fatal("agent with Client: want true")
	}

	withDynamic, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := withDynamic.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}
	if !tool.SuspendClientCallsInstalled(withDynamic) {
		t.Fatal("agent with DynamicClient: want true")
	}
}

func TestDynamicClientInstalledDistinguishesFromClient(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})

	bare, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if tool.DynamicClientInstalled(bare) {
		t.Fatal("bare agent: want false")
	}

	withClient, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := withClient.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}
	if tool.DynamicClientInstalled(withClient) {
		t.Fatal("agent with only Client (no DynamicClient): want false")
	}

	withDynamic, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := withDynamic.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}
	if !tool.DynamicClientInstalled(withDynamic) {
		t.Fatal("agent with DynamicClient: want true")
	}
}

func TestClientAndDynamicClientCannotBothInstall(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}
	if err := a.With(tool.DynamicClient()); err == nil {
		t.Fatal("installing DynamicClient after Client: want error, got nil")
	}
}

func TestDynamicClientStaleNameDoesNotResuspendOnLaterResume(t *testing.T) {
	// Regression: a name marked client-side by SetPendingClientTools on one
	// run/resume of a *State must not linger into a later run/resume of the
	// SAME state where SetPendingClientTools was not called again. If it
	// lingers, a call to that name on a later turn is wrongly classified
	// client-side and re-suspends the run even though nobody declared it
	// client-side this turn — a silent hang from the caller's perspective,
	// since it's waiting on a frontend nobody asked anything of.
	//
	// No registered server tool is involved here on purpose: the dispatch
	// middleware's client/registered-name collision panic (see
	// TestDynamicClientStaleNameDoesNotCollideWithServerToolOnLaterResume's
	// removal note below) fires unconditionally whenever a name is present
	// in the client-marked set and also registered — independent of
	// staleness, and even independent of whether that name is actually
	// called that turn. That makes it unsuitable for isolating the leak:
	// marking a registered name client-side panics on the very turn it's
	// marked, not on a later stale turn. This test isolates the leak itself
	// by using a name with no registered tool at all.
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			// Turn 1: get_location is client-marked via SetPendingClientTools
			// this turn, so the call suspends.
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{
			// Turn 2 (after Resume, no SetPendingClientTools this time): the
			// LLM calls get_location again. It must NOT be treated as
			// client-side, since nothing declared it this turn.
			ToolCalls:  []gantry.ToolCall{{ID: "q2", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}

	prior := &gantry.State{}
	if err := tool.SetPendingClientTools(prior, gantry.ToolDef{
		Name:        "get_location",
		Description: "client-side get_location for turn 1 only",
		Schema:      json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("SetPendingClientTools: %v", err)
	}

	suspended, err := a.RunFrom(context.Background(), prior, "where am I?")
	if err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend", suspended.Done, suspended.DoneReason)
	}

	// Fulfill the client call and clear terminal fields, then resume the SAME
	// state WITHOUT calling SetPendingClientTools again.
	suspended.Messages = append(suspended.Messages, gantry.Message{
		Role:       gantry.RoleTool,
		ToolCallID: suspended.PendingToolCalls[0].ID,
		Content:    `{"lat":0,"lng":0}`,
	})
	suspended.Done = false
	suspended.DoneReason = ""
	suspended.PendingToolCalls = nil

	final, err := a.Resume(context.Background(), suspended)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	// The bug: q2 gets wrongly classified client-side and the run suspends
	// again with DoneClientToolCall instead of running the rest of the loop
	// (the unhandled call falling through with no dispatcher installed, then
	// the LLM producing its normal final "done" response).
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "done" {
		t.Fatalf("Done=%q out=%q, want the run to continue past q2 (not re-suspend) to a normal finish",
			final.DoneReason, final.FinalOutput)
	}
	reqs := mock.Requests()
	if len(reqs) != 3 {
		t.Fatalf("got %d LLM requests, want 3 (turn 1, turn 2's get_location call, turn 2's continuation past it)", len(reqs))
	}
}

func TestSetPendingClientToolsEmptyNameReturnsError(t *testing.T) {
	prior := &gantry.State{}
	badDef := gantry.ToolDef{Name: "", Description: "bad", Schema: json.RawMessage(`{}`)}
	if err := tool.SetPendingClientTools(prior, badDef); err == nil {
		t.Fatal("empty tool name: want error, got nil")
	}
	if prior.Meta != nil {
		t.Fatalf("state.Meta = %#v, want untouched after validation failure", prior.Meta)
	}
}

func TestSetPendingClientToolsDuplicateNameReturnsError(t *testing.T) {
	prior := &gantry.State{}
	dupDef := gantry.ToolDef{Name: "same", Description: "dup", Schema: json.RawMessage(`{}`)}
	if err := tool.SetPendingClientTools(prior, dupDef, dupDef); err == nil {
		t.Fatal("duplicate tool name: want error, got nil")
	}
	if prior.Meta != nil {
		t.Fatalf("state.Meta = %#v, want untouched after validation failure", prior.Meta)
	}
}
