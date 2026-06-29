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
