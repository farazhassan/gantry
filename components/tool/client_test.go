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
	tool.WithClientTools(a, askDef())

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
	tool.WithTool(a, addOneTool{}) // executable, registered (from middleware_test.go)
	tool.WithClientTools(a, askDef())

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
	tool.WithTool(a, addOneTool{})

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls || state.FinalOutput != "final" {
		t.Fatalf("Done=%q out=%q, want normal finish", state.DoneReason, state.FinalOutput)
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
	tool.WithTool(a, addOneTool{})
	tool.WithClientTools(a, gantry.ToolDef{Name: "add_one", Description: "collide", Schema: json.RawMessage(`{}`)})

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on client/registered tool name collision")
		}
	}()
	_, _ = a.Run(context.Background(), "go")
}
