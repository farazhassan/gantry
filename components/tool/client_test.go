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

// TestMixedTurnRunsServerToolsAndSuspends proves tool.FromTools/tool.New
// composes with tool.Client regardless of which of the two installs first —
// both orders are exercised as subtests, mirroring
// TestDynamicClientComposesWithFromToolsInEitherOrder's shape (which covers
// the same thing for tool.DynamicClient). Before this, only
// from_tools_then_client was covered here.
func TestMixedTurnRunsServerToolsAndSuspends(t *testing.T) {
	for _, order := range []string{"from_tools_then_client", "client_then_from_tools"} {
		t.Run(order, func(t *testing.T) {
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
			client := tool.Client(askDef())
			fromTools := tool.FromTools(1, addOneTool{})
			var installErr error
			if order == "from_tools_then_client" {
				installErr = a.With(fromTools, client)
			} else {
				installErr = a.With(client, fromTools)
			}
			if installErr != nil {
				t.Fatalf("install (%s): %v", order, installErr)
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
		})
	}
}

// TestDynamicClientComposesWithFromToolsInEitherOrder is
// TestMixedTurnRunsServerToolsAndSuspends's DynamicClient counterpart: it
// proves tool.FromTools/tool.New (registryComponent's auto-installed
// SuspendClientCalls, see TestBareFromToolsSuspendsOnResumableToolWithoutClient)
// composes with tool.DynamicClient — not just tool.Client — regardless of
// which of the two installs first, since only Client's static-tool case was
// covered before.
func TestDynamicClientComposesWithFromToolsInEitherOrder(t *testing.T) {
	for _, order := range []string{"dynamic_client_then_from_tools", "from_tools_then_dynamic_client"} {
		t.Run(order, func(t *testing.T) {
			mock := eval.NewMockLLMClient(
				gantry.LLMResponse{
					ToolCalls: []gantry.ToolCall{
						{ID: "s1", Name: "add_one", Input: json.RawMessage(`5`)},
						{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)},
					},
					StopReason: gantry.StopReasonToolUse,
				},
			)
			a, _ := gantry.NewAgent(gantry.WithLLM(mock))
			dynamicClient := tool.DynamicClient()
			fromTools := tool.FromTools(1, addOneTool{})
			var installErr error
			if order == "dynamic_client_then_from_tools" {
				installErr = a.With(dynamicClient, fromTools)
			} else {
				installErr = a.With(fromTools, dynamicClient)
			}
			if installErr != nil {
				t.Fatalf("install (%s): %v", order, installErr)
			}

			prior := &gantry.State{}
			if err := tool.SetPendingClientTools(prior, gantry.ToolDef{
				Name:        "get_location",
				Description: "returns the browser's location",
				Schema:      json.RawMessage(`{}`),
			}); err != nil {
				t.Fatalf("SetPendingClientTools: %v", err)
			}

			state, err := a.RunFrom(context.Background(), prior, "go")
			if err != nil {
				t.Fatalf("RunFrom: %v", err)
			}
			if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
				t.Fatalf("Done=%v DoneReason=%q, want suspend", state.Done, state.DoneReason)
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
		})
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

func TestClientToolNameCollisionReturnsError(t *testing.T) {
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

	_, err := a.Run(context.Background(), "go")
	if err == nil {
		t.Fatal("expected an error on client/registered tool name collision, got nil")
	}
	if !strings.Contains(err.Error(), "add_one") {
		t.Fatalf("error = %v, want it to name the colliding tool", err)
	}
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

// TestDynamicClientAndClientCannotBothInstallReverseOrder is
// TestClientAndDynamicClientCannotBothInstall with the two components
// installed in the opposite order. Client.Install and DynamicClient.Install
// each now check SuspendClientCallsInstalled before installing it (so they
// compose order-independently with registryComponent's own auto-install —
// see TestMixedTurnRunsServerToolsAndSuspends), which means the mutual
// exclusion between Client and DynamicClient can no longer rely on that
// shared-middleware-name collision to fire in this direction. It is enforced
// explicitly instead (clientInstalled/DynamicClientInstalled checks in each
// Install), and this proves that guard actually fires for both install
// orders, not just the one exercised above.
func TestDynamicClientAndClientCannotBothInstallReverseOrder(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.DynamicClient()); err != nil {
		t.Fatalf("install dynamic client: %v", err)
	}
	if err := a.With(tool.Client(askDef())); err == nil {
		t.Fatal("installing Client after DynamicClient: want error, got nil")
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

// TestSuspendClientCallsRejectsOriginCallIDContainingSeparator guards
// against a provider-generated tool-call ID that happens to already contain
// pendingIDSep ("\x1f", the separator SuspendClientCalls uses to build the
// composite ID surfaced in PendingToolCalls) silently corrupting the
// composite-ID scheme: splitPendingID only ever recovers the prefix up to
// the FIRST separator as the origin, so the entries map (keyed by the real,
// full call ID) would become unreachable from the surfaced composite ID.
// This must be caught at the point the composite ID is built: the offending
// origin's whole PendingResult folds as a normal tool error instead of
// suspending on a broken ID, while any other, well-formed pending call in
// the same batch is processed normally — proven here with a second,
// well-formed ResumableTool call in the same turn.
//
// Deliberately not covering a Pending[i].ID (as opposed to the originating
// CallID) containing the separator: multi-level nesting (an agent-as-tool
// delegate whose own child already suspended) legitimately produces exactly
// that — see pendingIDSepContractErr's doc comment in pending.go and
// components/subagent's TestTwoLevelNestedSuspendAndResume, which relies on
// it.
func TestSuspendClientCallsRejectsOriginCallIDContainingSeparator(t *testing.T) {
	const sep = "\x1f" // must match components/tool's unexported pendingIDSep
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c" + sep + "bad", Name: "specialist_bad", Input: json.RawMessage(`{}`)},
				{ID: "cgood", Name: "specialist_good", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client()); err != nil {
		t.Fatalf("install client: %v", err)
	}
	badSpecialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist_bad", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "leaf1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
	}
	goodSpecialist := &fakeResumable{
		def: gantry.ToolDef{Name: "specialist_good", Description: "d", Schema: json.RawMessage(`{}`)},
		invokeErr: &gantry.PendingResult{
			Pending: []gantry.ToolCall{{ID: "ok1", Name: "ask_user", Input: json.RawMessage(`{}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
	}
	if err := a.With(tool.FromTools(1, badSpecialist, goodSpecialist)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend (the good specialist's leaf is still pending)", state.Done, state.DoneReason)
	}

	// The bad origin's leaf must never be surfaced as a pending call — it
	// would carry a corrupted composite ID.
	if len(state.PendingToolCalls) != 1 {
		t.Fatalf("PendingToolCalls = %#v, want exactly 1 (only the good specialist's leaf)", state.PendingToolCalls)
	}
	pc := state.PendingToolCalls[0]
	if strings.Contains(pc.ID, "bad") || !strings.Contains(pc.ID, "cgood") {
		t.Errorf("surfaced pending call = %+v, want it to be the good specialist's leaf, not the bad one's", pc)
	}

	// The bad origin must instead get a normal folded tool-error message,
	// keyed by its own (separator-containing) CallID.
	badOriginID := "c" + sep + "bad"
	var badMsg *gantry.Message
	for i, m := range state.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == badOriginID {
			badMsg = &state.Messages[i]
		}
		if m.Role == gantry.RoleTool && m.ToolCallID == "cgood" {
			t.Errorf("cgood must not have a folded tool result while still pending; messages: %+v", state.Messages)
		}
	}
	if badMsg == nil {
		t.Fatalf("no tool_result message for the bad origin in state.Messages: %+v (transcript corruption: a tool_use with no matching tool_result)", state.Messages)
	}
	if !strings.Contains(badMsg.Content, "specialist_bad") || !strings.Contains(badMsg.Content, "separator") {
		t.Errorf("tool_result content for the bad origin = %q, want it to identify the tool and the reserved-separator problem", badMsg.Content)
	}
}

// TestSuspendClientCallsRejectsFlatCallIDContainingSeparator guards the
// other point of origin pendingIDSepContractErr's doc comment identifies as
// unvalidated by that check alone: a declared client-tool call
// (tool.Client/tool.DynamicClient) never goes through the composite-ID
// construction site that check guards (it has no server-side Invoke, so it
// never produces a ToolResult), so a pathological provider-generated ID
// containing pendingIDSep would otherwise reach PendingToolCalls completely
// unvalidated. Unlike a PendingResult.Pending[i].ID, a flat call's own ID is
// always this level's freshly-dispatched LLM tool_call ID — never
// legitimately composite — so it's safe to reject centrally here, the same
// way the origin CallID case is. Proven here with a second, well-formed
// client-tool call in the same turn to confirm only the bad one is affected.
func TestSuspendClientCallsRejectsFlatCallIDContainingSeparator(t *testing.T) {
	const sep = "\x1f" // must match components/tool's unexported pendingIDSep
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c" + sep + "bad", Name: "ask_user", Input: json.RawMessage(`{}`)},
				{ID: "cgood", Name: "ask_user", Input: json.RawMessage(`{}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.Client(askDef())); err != nil {
		t.Fatalf("install client: %v", err)
	}

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend (the good call is still pending)", state.Done, state.DoneReason)
	}

	// The bad call must never be surfaced as still-pending.
	if len(state.PendingToolCalls) != 1 || state.PendingToolCalls[0].ID != "cgood" {
		t.Fatalf("PendingToolCalls = %#v, want exactly the good call (cgood)", state.PendingToolCalls)
	}

	badID := "c" + sep + "bad"
	var badMsg *gantry.Message
	for i, m := range state.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == badID {
			badMsg = &state.Messages[i]
		}
		if m.Role == gantry.RoleTool && m.ToolCallID == "cgood" {
			t.Errorf("cgood must not have a folded tool result while still pending; messages: %+v", state.Messages)
		}
	}
	if badMsg == nil {
		t.Fatalf("no tool_result message for the bad call in state.Messages: %+v (transcript corruption: a tool_use with no matching tool_result)", state.Messages)
	}
	if !strings.Contains(badMsg.Content, "ask_user") || !strings.Contains(badMsg.Content, "separator") {
		t.Errorf("tool_result content for the bad call = %q, want it to identify the tool and the reserved-separator problem", badMsg.Content)
	}
}

// TestSuspendClientCallsTypedNilPendingResultPassesThroughNotPanics guards
// SuspendClientCalls's PhaseObserve loop over s.ToolResults against the same
// footgun other tests in this file guard elsewhere in the pipeline: a
// ToolResult whose Err is a non-nil error interface wrapping a nil
// *gantry.PendingResult pointer still matches errors.As, but dereferencing
// pending.Resume on it would panic. The ToolResult is constructed directly
// (via a custom PhaseToolExec middleware standing in for the normal
// dispatch/registry pipeline) so this test exercises SuspendClientCalls in
// isolation, regardless of whether an upstream fix (Registry.Invoke,
// dispatch) already prevents such a value from arising through the normal
// tool-call path. A typed-nil PendingResult is not a genuine suspend signal,
// so the result must pass through to the transcript unchanged, and the run
// must finish normally rather than suspend.
func TestSuspendClientCallsTypedNilPendingResultPassesThroughNotPanics(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "buggy", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.SuspendClientCalls()); err != nil {
		t.Fatalf("install SuspendClientCalls: %v", err)
	}

	var nilPending *gantry.PendingResult
	var wrapped error = nilPending // non-nil interface wrapping a nil *PendingResult
	a.Use(gantry.PhaseToolExec, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.ToolResults = append(s.ToolResults, gantry.ToolResult{
				CallID:  "c1",
				Content: "unaffected content",
				Err:     wrapped,
			})
			return next(ctx, s)
		}
	})

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.Done && state.DoneReason == gantry.DoneClientToolCall {
		t.Fatalf("run suspended (DoneReason=%q) on a typed-nil PendingResult; want it passed through as a normal result and the run to continue", state.DoneReason)
	}
	if state.DoneReason != gantry.DoneNoToolCalls || state.FinalOutput != "done" {
		t.Fatalf("Done=%v DoneReason=%q FinalOutput=%q, want a normal finish after a second LLM turn", state.Done, state.DoneReason, state.FinalOutput)
	}

	var found bool
	for _, m := range state.Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" {
			found = true
			if m.Content != "unaffected content" {
				t.Errorf("tool_result content = %q, want the original ToolResult.Content passed through unchanged", m.Content)
			}
		}
	}
	if !found {
		t.Fatalf("no tool_result message for c1 in state.Messages: %+v (transcript corruption: a tool_use with no matching tool_result)", state.Messages)
	}
}

func TestResumableToolSuspendSurfacesOnPendingToolCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	// tool.Client() with no defs installs suspend detection without
	// declaring any client-side name. Redundant here since tool.FromTools
	// below now installs it automatically too, but kept to also cover the
	// case of an agent that installs Client explicitly alongside a registry.
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

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !state.Done || state.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend", state.Done, state.DoneReason)
	}
	if len(state.PendingToolCalls) != 1 {
		t.Fatalf("PendingToolCalls = %#v, want exactly 1", state.PendingToolCalls)
	}
	pc := state.PendingToolCalls[0]
	if pc.Name != "ask_user" || string(pc.Input) != `{"q":"ok?"}` {
		t.Errorf("surfaced call = %+v, want the leaf's real Name/Input untouched", pc)
	}
	if pc.ID == "ask1" || pc.ID == "c1" {
		t.Errorf("surfaced ID %q should be a composite of the originating call and the leaf, not either alone", pc.ID)
	}
	for _, m := range state.Messages {
		if m.ToolCallID == "c1" {
			t.Errorf("originating call c1 must not have a folded tool result while pending; messages: %+v", state.Messages)
		}
	}
}

// TestSuspendClientCallsEmitsToolPendingEventForNestedSuspend guards the gap
// GitHub Copilot flagged on PR #80: TestResumableToolSuspendSurfacesOnPendingToolCalls
// above proves a nested ResumableTool suspend lands the leaf's composite
// ID/Name/Input on state.PendingToolCalls, but a RunStream consumer never
// sees a *State — only Events — and until now nothing streamed that leaf's
// composite ID/Name/Input at all: PhasePostLLM's EventToolCall emission (see
// emitPhaseEffects in run_stream.go) fires before SuspendClientCalls'
// PhaseObserve middleware ever builds the composite ID, so a live consumer
// only ever saw the original "specialist" tool_call and had no way to
// discover what its suspended child actually needs answered.
//
// This test proves the fix (SuspendClientCalls now emits EventToolPending
// for each newly-surfaced leaf) is sufficient for a streaming consumer to
// act: it builds the Resume answer's CallID entirely from the composite ID
// captured off the EventToolPending event, never touching
// suspended.PendingToolCalls, and confirms that answer actually resolves
// the suspended specialist and lets the run finish.
func TestSuspendClientCallsEmitsToolPendingEventForNestedSuspend(t *testing.T) {
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
			Pending: []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			Resume:  json.RawMessage(`{"step":1}`),
		},
		resumeFn: func(resume json.RawMessage, results []gantry.ToolResult) (json.RawMessage, error) {
			if len(results) != 1 || results[0].Content != "Ada" {
				t.Errorf("Resume got results %#v, want the ask1 answer", results)
			}
			return json.RawMessage(`{"output":"specialist finished"}`), nil
		},
	}
	reg.Add(specialist)
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tools: %v", err)
	}

	var events []gantry.Event
	suspended, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("Done=%v DoneReason=%q, want suspend", suspended.Done, suspended.DoneReason)
	}

	var pendingEvents []gantry.Event
	for _, ev := range events {
		if ev.Type == gantry.EventToolPending {
			pendingEvents = append(pendingEvents, ev)
		}
	}
	if len(pendingEvents) != 1 {
		t.Fatalf("EventToolPending count = %d, want exactly 1; events: %#v", len(pendingEvents), events)
	}
	tc := pendingEvents[0].ToolCall
	if tc == nil {
		t.Fatalf("EventToolPending has nil ToolCall")
	}
	if tc.Name != "ask_user" || string(tc.Input) != `{"q":"name?"}` {
		t.Errorf("EventToolPending ToolCall = %+v, want the leaf's real Name/Input", tc)
	}
	if tc.ID == "ask1" || tc.ID == "c1" {
		t.Errorf("EventToolPending ToolCall.ID %q should be a composite of the originating call and the leaf, not either alone", tc.ID)
	}

	// The event must never masquerade as a finished result: no EventToolResult
	// for the still-pending leaf, and the still-pending origin c1 must not
	// yet have a folded tool result in the transcript either.
	eventCompositeID := tc.ID
	for _, ev := range events {
		if ev.Type == gantry.EventToolResult && ev.ToolResult != nil && ev.ToolResult.CallID == eventCompositeID {
			t.Errorf("EventToolResult fired for still-pending leaf %q; a pending call must never be reported as finished", eventCompositeID)
		}
	}
	for _, m := range suspended.Messages {
		if m.ToolCallID == "c1" {
			t.Errorf("originating call c1 must not have a folded tool result while still pending; messages: %+v", suspended.Messages)
		}
	}

	// A real streaming consumer never sees suspended.PendingToolCalls — build
	// the answer purely from what the event surfaced.
	final, err := tool.Resume(context.Background(), a, reg, suspended, []gantry.ToolResult{
		{CallID: eventCompositeID, Content: "Ada"},
	})
	if err != nil {
		t.Fatalf("Resume using the event-derived composite ID: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "used the specialist's answer" {
		t.Fatalf("Done=%q out=%q, want a normal finish", final.DoneReason, final.FinalOutput)
	}
}
