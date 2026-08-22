// components/subagent/nested_resume_test.go
package subagent

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func TestOneLevelNestedSuspendAndResume(t *testing.T) {
	childMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "plan executed", StopReason: gantry.StopReasonEnd},
	)
	child, err := gantry.NewAgent(gantry.WithLLM(childMock))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	delegate := New("planner", "plans, asking before executing", child)

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "planner", Input: json.RawMessage(`{"goal":"ship it"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "the planner reports: plan executed", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(1, delegate)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !suspended.Done || suspended.DoneReason != gantry.DoneClientToolCall {
		t.Fatalf("parent Done=%v DoneReason=%q, want suspended by the child's ask_user call", suspended.Done, suspended.DoneReason)
	}
	if len(suspended.PendingToolCalls) != 1 || suspended.PendingToolCalls[0].Name != "ask_user" {
		t.Fatalf("parent PendingToolCalls = %#v, want the child's ask_user call surfaced", suspended.PendingToolCalls)
	}
	for _, m := range suspended.Messages {
		if m.ToolCallID == "c1" {
			t.Errorf("originating planner call c1 must not have a folded result while its child is pending")
		}
	}

	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, []gantry.ToolResult{
		{CallID: suspended.PendingToolCalls[0].ID, Content: `{"answer":"yes"}`},
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "the planner reports: plan executed" {
		t.Fatalf("Done=%q out=%q, want the parent to finish normally using the planner's completed output", final.DoneReason, final.FinalOutput)
	}
	if len(parentMock.Requests()) != 2 {
		t.Fatalf("parent LLM called %d times, want 2 (dispatch + continuation after resume)", len(parentMock.Requests()))
	}
	if len(childMock.Requests()) != 2 {
		t.Fatalf("child LLM called %d times, want 2 (initial call + continuation after its own answer)", len(childMock.Requests()))
	}
}

func TestTwoSiblingDelegateCallsSuspendAndResumeIndependently(t *testing.T) {
	newAskingChild := func(t *testing.T, question string) (*gantry.Agent, *eval.MockLLMClient) {
		t.Helper()
		mock := eval.NewMockLLMClient(
			gantry.LLMResponse{
				ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"` + question + `"}`)}},
				StopReason: gantry.StopReasonToolUse,
			},
			gantry.LLMResponse{Content: "done: " + question, StopReason: gantry.StopReasonEnd},
		)
		c, err := gantry.NewAgent(gantry.WithLLM(mock))
		if err != nil {
			t.Fatalf("NewAgent: %v", err)
		}
		if err := c.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
			t.Fatalf("install client tools: %v", err)
		}
		return c, mock
	}

	childA, mockA := newAskingChild(t, "A?")
	childB, mockB := newAskingChild(t, "B?")
	delegateA := New("agentA", "d", childA)
	delegateB := New("agentB", "d", childB)

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c1", Name: "agentA", Input: json.RawMessage(`{"goal":"a"}`)},
				{ID: "c2", Name: "agentB", Input: json.RawMessage(`{"goal":"b"}`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "both done", StopReason: gantry.StopReasonEnd},
	)
	parentComp, parentReg := ComponentWithRegistry(2, delegateA, delegateB)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentMock), gantry.WithComponents(parentComp))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	suspended, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(suspended.PendingToolCalls) != 2 {
		t.Fatalf("PendingToolCalls = %#v, want 2 (one per sibling delegate)", suspended.PendingToolCalls)
	}
	for _, m := range suspended.Messages {
		if m.ToolCallID == "c1" || m.ToolCallID == "c2" {
			t.Errorf("originating delegate call %s must not have a folded result while its child is pending", m.ToolCallID)
		}
	}

	// Give each sibling a distinguishable answer so that a routing bug that
	// cross-wires the two children's answers is actually detectable below,
	// rather than being masked by both children being handed the same
	// content.
	const answerA = `{"answer":"yes-for-A"}`
	const answerB = `{"answer":"yes-for-B"}`
	answers := make([]gantry.ToolResult, len(suspended.PendingToolCalls))
	for i, pc := range suspended.PendingToolCalls {
		switch {
		case bytes.Contains(pc.Input, []byte("A?")):
			answers[i] = gantry.ToolResult{CallID: pc.ID, Content: answerA}
		case bytes.Contains(pc.Input, []byte("B?")):
			answers[i] = gantry.ToolResult{CallID: pc.ID, Content: answerB}
		default:
			t.Fatalf("pending call %#v does not match either sibling's question, cannot assign an answer", pc)
		}
	}
	final, err := tool.Resume(context.Background(), parent, parentReg, suspended, answers)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if final.DoneReason != gantry.DoneNoToolCalls || final.FinalOutput != "both done" {
		t.Fatalf("Done=%q out=%q, want both siblings to finish and the parent to continue", final.DoneReason, final.FinalOutput)
	}

	// Prove the answers were not cross-wired: each child's own second LLM
	// request (its continuation after being resumed) must carry the
	// tool-result message that was actually addressed to it, and must not
	// carry the sibling's answer instead.
	requireOwnAnswer := func(t *testing.T, who string, mock *eval.MockLLMClient, want string) {
		t.Helper()
		reqs := mock.Requests()
		if len(reqs) != 2 {
			t.Fatalf("%s LLM called %d times, want 2 (initial call + continuation after its own answer)", who, len(reqs))
		}
		var got string
		found := false
		for _, m := range reqs[1].Messages {
			if m.Role == gantry.RoleTool && m.ToolCallID == "ask1" {
				got = m.Content
				found = true
			}
		}
		if !found {
			t.Fatalf("%s second LLM request has no RoleTool message for ask1; messages=%#v", who, reqs[1].Messages)
		}
		if got != want {
			t.Errorf("%s received answer %q, want %q (its own answer, not its sibling's)", who, got, want)
		}
	}
	requireOwnAnswer(t, "childA", mockA, answerA)
	requireOwnAnswer(t, "childB", mockB, answerB)
}
