package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

type addOneTool struct{}

func (addOneTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "add_one", Description: "adds one", Schema: json.RawMessage(`{}`)}
}

func (addOneTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var n int
	if err := json.Unmarshal(input, &n); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(n + 1)
	return out, nil
}

func TestWithToolRegistersDefinitionAtStart(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "add_one" {
		t.Errorf("Tools not registered in LLM request: %+v", reqs[0].Tools)
	}
}

func TestWithToolDispatchesPendingCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "add_one", Input: json.RawMessage(`5`)}},
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
	if state.FinalOutput != "final" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	found := false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" && m.Content == "6" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected tool result with CallID c1 and content '6'; messages: %+v", reqs[1].Messages)
	}
}

func TestWithToolsParallelDispatch(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "a", Name: "add_one", Input: json.RawMessage(`1`)},
				{ID: "b", Name: "add_one", Input: json.RawMessage(`2`)},
				{ID: "c", Name: "add_one", Input: json.RawMessage(`3`)},
			},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(2, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	results := map[string]string{}
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool {
			results[m.ToolCallID] = m.Content
		}
	}
	for callID, want := range map[string]string{"a": "2", "b": "3", "c": "4"} {
		if results[callID] != want {
			t.Errorf("call %q result = %q, want %q", callID, results[callID], want)
		}
	}
}

func TestWithToolUnknownToolRecordsError(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "g", Name: "ghost"}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	found := false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "g" {
			found = true
			if m.Content == "" {
				t.Errorf("expected error content in tool result")
			}
		}
	}
	if !found {
		t.Errorf("missing tool result for unknown tool")
	}
}

// addTwoTool is a second tool used to prove runtime reg.Add is visible.
type addTwoTool struct{}

func (addTwoTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "add_two", Description: "adds two", Schema: json.RawMessage(`{}`)}
}

func (addTwoTool) Invoke(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
	var n int
	if err := json.Unmarshal(input, &n); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(n + 2)
	return out, nil
}

func TestWithRegistryHappyPath(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	reg := tool.NewRegistry()
	reg.Add(addOneTool{})
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	reqs := mock.Requests()
	if len(reqs) != 1 || len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "add_one" {
		t.Errorf("registry tools not advertised: %+v", reqs)
	}
}

func TestWithRegistrySharedAcrossAgents(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(addOneTool{})

	mockA := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	mockB := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mockA))
	b, _ := gantry.NewAgent(gantry.WithLLM(mockB))

	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool on a: %v", err)
	}
	if err := b.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool on b: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run a: %v", err)
	}
	if _, err := b.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run b: %v", err)
	}

	reqA := mockA.Requests()
	if len(reqA) != 1 || len(reqA[0].Tools) != 1 || reqA[0].Tools[0].Name != "add_one" {
		t.Errorf("agent a: shared registry tool not visible: %+v", reqA)
	}
	reqB := mockB.Requests()
	if len(reqB) != 1 || len(reqB[0].Tools) != 1 || reqB[0].Tools[0].Name != "add_one" {
		t.Errorf("agent b: shared registry tool not visible: %+v", reqB)
	}
}

func TestWithRegistryRuntimeAddVisibleNextRun(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(addOneTool{})

	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd},
	)
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("install tool: %v", err)
	}

	if _, err := a.Run(context.Background(), "first"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	reg.Add(addTwoTool{}) // mutate the registry between runs
	if _, err := a.Run(context.Background(), "second"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	if len(reqs[0].Tools) != 1 {
		t.Errorf("run 1 should advertise 1 tool, got %+v", reqs[0].Tools)
	}
	names := map[string]bool{}
	for _, td := range reqs[1].Tools {
		names[td.Name] = true
	}
	if !names["add_one"] || !names["add_two"] {
		t.Errorf("run 2 should advertise add_one and add_two, got %+v", reqs[1].Tools)
	}
}

func TestToolDoubleInstallReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	reg := tool.NewRegistry()
	reg.Add(addOneTool{})
	if err := a.With(tool.New(reg, 1)); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.New(reg, 1)); err == nil {
		t.Fatal("second install: want error, got nil")
	}
}

func TestFromToolsDoubleInstallReturnsError(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	if err := a.With(tool.FromTools(1, addOneTool{})); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(tool.FromTools(1, addOneTool{})); err == nil {
		t.Fatal("second install: want error, got nil")
	}
}
