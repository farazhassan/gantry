package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestDefaultLLMCallHandler(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "hello",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 10, OutputTokens: 5},
	})
	state := gantry.NewState("hi")
	state.Messages = []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}

	handler := gantry.DefaultLLMCallHandler(mock)
	if err := handler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if state.LastResponse == nil || state.LastResponse.Content != "hello" {
		t.Errorf("LastResponse = %+v", state.LastResponse)
	}
	if state.Usage.InputTokens != 10 {
		t.Errorf("Usage.InputTokens = %d, want 10", state.Usage.InputTokens)
	}
}

func TestDefaultPostLLMHandlerNoToolCallsSetsDone(t *testing.T) {
	state := gantry.NewState("hi")
	state.LastResponse = &gantry.LLMResponse{
		Content:    "answer",
		StopReason: gantry.StopReasonEnd,
	}
	if err := gantry.DefaultPostLLMHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if !state.Done {
		t.Errorf("expected Done=true with no tool calls")
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
	if state.FinalOutput != "answer" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
}

func TestDefaultPostLLMHandlerWithToolCallsKeepsRunning(t *testing.T) {
	state := gantry.NewState("hi")
	state.LastResponse = &gantry.LLMResponse{
		StopReason: gantry.StopReasonToolUse,
		ToolCalls:  []gantry.ToolCall{{ID: "t1", Name: "search"}},
	}
	if err := gantry.DefaultPostLLMHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if state.Done {
		t.Errorf("expected Done=false with tool calls pending")
	}
	if len(state.PendingToolCalls) != 1 {
		t.Errorf("PendingToolCalls len = %d, want 1", len(state.PendingToolCalls))
	}
}

func TestDefaultStartHandlerSeedsUserInput(t *testing.T) {
	state := gantry.NewState("hello there")
	if err := gantry.DefaultStartHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if len(state.Messages) != 1 {
		t.Fatalf("Messages len = %d, want 1", len(state.Messages))
	}
	if state.Messages[0].Role != gantry.RoleUser || state.Messages[0].Content != "hello there" {
		t.Errorf("messages[0] = %+v", state.Messages[0])
	}
}

func TestDefaultStartHandlerSkipsWhenMessagesPresent(t *testing.T) {
	state := gantry.NewState("ignored")
	state.Messages = []gantry.Message{{Role: gantry.RoleUser, Content: "preloaded"}}
	if err := gantry.DefaultStartHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if len(state.Messages) != 1 || state.Messages[0].Content != "preloaded" {
		t.Errorf("messages should be untouched; got %+v", state.Messages)
	}
}

func TestDefaultStartHandlerSkipsWhenInputEmpty(t *testing.T) {
	state := gantry.NewState("")
	if err := gantry.DefaultStartHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if len(state.Messages) != 0 {
		t.Errorf("expected empty messages; got %+v", state.Messages)
	}
}

func TestDefaultObserveHandlerFoldsResults(t *testing.T) {
	state := gantry.NewState("hi")
	state.ToolResults = []gantry.ToolResult{
		{CallID: "t1", Content: "result-a"},
		{CallID: "t2", Content: "result-b", IsError: true},
	}
	if err := gantry.DefaultObserveHandler(context.Background(), state); err != nil {
		t.Fatalf("handler err: %v", err)
	}
	if len(state.Messages) != 2 {
		t.Fatalf("Messages len = %d, want 2", len(state.Messages))
	}
	if state.Messages[0].Role != gantry.RoleTool || state.Messages[0].ToolCallID != "t1" {
		t.Errorf("messages[0] = %+v", state.Messages[0])
	}
	if state.Messages[1].Content != "result-b" {
		t.Errorf("messages[1].Content = %q", state.Messages[1].Content)
	}
	if len(state.ToolResults) != 0 {
		t.Errorf("ToolResults should be cleared after folding; got %+v", state.ToolResults)
	}
	if len(state.PendingToolCalls) != 0 {
		t.Errorf("PendingToolCalls should be cleared after folding; got %+v", state.PendingToolCalls)
	}
}
