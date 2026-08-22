package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

// newChildAgent builds a child agent driven by a scripted mock LLM. Shared by
// the delegate, run-context, and component tests (same package).
func newChildAgent(t *testing.T, responses ...gantry.LLMResponse) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(responses...)))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	return a
}

func TestDefinition(t *testing.T) {
	child := newChildAgent(t)
	def := New("research_specialist", "delegates research to a specialist", child).Definition()

	if def.Name != "research_specialist" {
		t.Errorf("Name = %q, want research_specialist", def.Name)
	}
	if def.Description != "delegates research to a specialist" {
		t.Errorf("Description = %q, want the constructor description", def.Description)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(def.Schema, &schema); err != nil {
		t.Fatalf("Schema is not valid JSON: %v", err)
	}
	if _, ok := schema.Properties["goal"]; !ok {
		t.Errorf("schema missing 'goal' property")
	}
	if _, ok := schema.Properties["context"]; !ok {
		t.Errorf("schema missing 'context' property")
	}
	if len(schema.Required) != 1 || schema.Required[0] != "goal" {
		t.Errorf("Required = %v, want [goal]", schema.Required)
	}
}

func TestInvokeReturnsChildFinalOutput(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{
		Content:    "the capital is Ottawa",
		StopReason: gantry.StopReasonEnd,
	})
	tl := New("research_specialist", "d", child)

	out, err := tl.Invoke(context.Background(), json.RawMessage(`{"goal":"capital of Canada?"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if res.Output != "the capital is Ottawa" {
		t.Errorf("output = %q, want the child's FinalOutput", res.Output)
	}
}

func TestInvokeSeedsGoalAndContextAsUserMessage(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	child, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	tl := New("specialist", "d", child)

	_, err = tl.Invoke(context.Background(),
		json.RawMessage(`{"goal":"summarize the findings","context":"finding A; finding B"}`))
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("child saw %d LLM requests, want 1", len(reqs))
	}
	msgs := reqs[0].Messages
	if len(msgs) != 1 {
		t.Fatalf("child transcript has %d messages, want 1 (isolated context, no parent history)", len(msgs))
	}
	if msgs[0].Role != gantry.RoleUser {
		t.Errorf("seed role = %q, want %q", msgs[0].Role, gantry.RoleUser)
	}
	want := "summarize the findings\n\nContext:\nfinding A; finding B"
	if msgs[0].Content != want {
		t.Errorf("seed content = %q, want %q", msgs[0].Content, want)
	}
}

func TestInvokeGoalOnlySeedsBareGoal(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	child, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	tl := New("specialist", "d", child)

	if _, err := tl.Invoke(context.Background(), json.RawMessage(`{"goal":"just the goal"}`)); err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Messages) != 1 || reqs[0].Messages[0].Content != "just the goal" {
		t.Errorf("child requests = %+v, want a single bare-goal user message", reqs)
	}
}

func TestInvokeMalformedInputIsError(t *testing.T) {
	tl := New("specialist", "d", newChildAgent(t))
	if _, err := tl.Invoke(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Errorf("Invoke with malformed input = nil error, want an error")
	}
}

func TestInvokeEmptyGoalIsError(t *testing.T) {
	tl := New("specialist", "d", newChildAgent(t))
	if _, err := tl.Invoke(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Errorf("Invoke with empty goal = nil error, want an error")
	}
}

func TestInvokeChildErrorIsToolError(t *testing.T) {
	llm := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: errors.New("llm boom")}})
	child, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	tl := New("specialist", "d", child)

	_, err = tl.Invoke(context.Background(), json.RawMessage(`{"goal":"g"}`))
	if err == nil {
		t.Fatalf("Invoke = nil error, want the child's run error")
	}
	if !strings.Contains(err.Error(), "llm boom") {
		t.Errorf("err = %v, want it to wrap the child's LLM error", err)
	}
}

func TestInvokeNilChildIsError(t *testing.T) {
	tl := New("specialist", "d", nil)
	if _, err := tl.Invoke(context.Background(), json.RawMessage(`{"goal":"g"}`)); err == nil {
		t.Errorf("Invoke with nil child = nil error, want a configuration error")
	}
}

func TestInvokeThreadsParentLinkWhenAmbientIdentityPresent(t *testing.T) {
	childMock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	child, err := gantry.NewAgent(gantry.WithLLM(childMock), gantry.WithName("investigation"))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	tl := New("investigate", "d", child, WithEventPassthrough())

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "call-9", Name: "investigate", Input: json.RawMessage(`{"goal":"g"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	parent, err := gantry.NewAgent(
		gantry.WithLLM(parentMock),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(tool.FromTools(1, tl)),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	var events []gantry.Event
	_, err = parent.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}

	var childEvents []gantry.Event
	for _, ev := range events {
		if ev.Agent == "investigation" {
			childEvents = append(childEvents, ev)
		}
	}
	if len(childEvents) == 0 {
		t.Fatal("passthrough enabled: expected the child's own events on the parent's stream, got none")
	}

	var parentRunID string
	for _, ev := range events {
		if ev.Agent == "orchestrator" {
			parentRunID = ev.RunID
			break
		}
	}
	for i, ev := range childEvents {
		if ev.ParentRunID != parentRunID {
			t.Errorf("child event %d ParentRunID = %q, want %q (the orchestrator's own RunID)", i, ev.ParentRunID, parentRunID)
		}
		if ev.ParentToolCallID != "call-9" {
			t.Errorf("child event %d ParentToolCallID = %q, want call-9", i, ev.ParentToolCallID)
		}
	}
}

func TestInvokeReturnsPendingResultWhenChildSuspends(t *testing.T) {
	askMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "ask1", Name: "ask_user", Input: json.RawMessage(`{"q":"proceed?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
	)
	child, err := gantry.NewAgent(gantry.WithLLM(askMock))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	if err := child.With(tool.Client(gantry.ToolDef{Name: "ask_user", Description: "d", Schema: json.RawMessage(`{}`)})); err != nil {
		t.Fatalf("install client tools on child: %v", err)
	}
	tl := New("planner", "plans, asking before executing", child)

	if _, ok := tl.(tool.ResumableTool); !ok {
		t.Fatal("delegate tool does not implement tool.ResumableTool")
	}

	_, err = tl.Invoke(context.Background(), json.RawMessage(`{"goal":"plan the release"}`))
	if err == nil {
		t.Fatal("Invoke = nil error, want a *gantry.PendingResult when the child suspends")
	}
	var pending *gantry.PendingResult
	if !errors.As(err, &pending) {
		t.Fatalf("Invoke err = %v, want it to be a *gantry.PendingResult", err)
	}
	if len(pending.Pending) != 1 || pending.Pending[0].Name != "ask_user" {
		t.Fatalf("Pending = %#v, want the child's own ask_user call", pending.Pending)
	}
	if len(pending.Resume) == 0 {
		t.Error("Resume token is empty, want the marshaled suspended child state")
	}
	// tl.(tool.ResumableTool).Resume is exercised end-to-end by
	// TestOneLevelNestedSuspendAndResume (Task 11) and
	// TestTwoLevelNestedSuspendAndResume (Task 12) — not this task.
}

func TestInvokeWithoutPassthroughStillHasNoAmbientEvents(t *testing.T) {
	childMock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	child, err := gantry.NewAgent(gantry.WithLLM(childMock), gantry.WithName("investigation"))
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}
	tl := New("investigate", "d", child) // no WithEventPassthrough

	parentMock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "call-9", Name: "investigate", Input: json.RawMessage(`{"goal":"g"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	parent, err := gantry.NewAgent(
		gantry.WithLLM(parentMock),
		gantry.WithName("orchestrator"),
		gantry.WithComponents(tool.FromTools(1, tl)),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	var sawChildEvent bool
	_, err = parent.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Agent == "investigation" {
			sawChildEvent = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if sawChildEvent {
		t.Error("passthrough disabled: saw a child event on the parent's stream, want none")
	}
}
