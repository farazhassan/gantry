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
