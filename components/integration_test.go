package components_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/components/retriever"
	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

type calcTool struct{}

func (calcTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{Name: "calc", Description: "adds two ints", Schema: json.RawMessage(`{}`)}
}

func (calcTool) Invoke(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var args struct{ A, B int }
	if err := json.Unmarshal(in, &args); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(args.A + args.B)
	return out, nil
}

// This test wires Memory, Skill, Retriever, Compactor, Tool, Limiter, and
// Guardrail together and verifies the agent completes successfully without
// any component interfering with another. It uses two LLM turns: first
// requesting a tool call, second producing the final answer.
func TestComponentsInteroperate(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls: []gantry.ToolCall{
				{ID: "c1", Name: "calc", Input: json.RawMessage(`{"A":2,"B":3}`)},
			},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 100, OutputTokens: 50},
		},
		gantry.LLMResponse{
			Content:    "answer is 5",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 30, OutputTokens: 10},
		},
	)

	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(5))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	memory.WithMemory(a, memory.NewInMemoryStore())
	if err := a.With(skill.New(skill.NewStatic("careful", "Be careful with numbers."))); err != nil {
		t.Fatalf("install skill: %v", err)
	}
	if err := a.With(retriever.New(retriever.NewStatic([]gantry.Document{
		{ID: "doc1", Content: "context: arithmetic is good"},
	}), 5)); err != nil {
		t.Fatalf("install retriever: %v", err)
	}
	if err := a.With(compactor.New(compactor.NewSlidingWindow(10), compactor.Budget{})); err != nil {
		t.Fatalf("install compactor: %v", err)
	}
	tool.WithTool(a, calcTool{})
	if err := a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 1000}))); err != nil {
		t.Fatalf("install limiter: %v", err)
	}
	if err := a.With(guardrail.New(guardrail.NewRegex(`forbidden`, guardrail.DirectionOutput))); err != nil {
		t.Fatalf("install guardrail: %v", err)
	}

	state, err := a.Run(context.Background(), "what is 2 + 3?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "answer is 5" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q", state.DoneReason)
	}
	if state.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", state.Iteration)
	}

	// Sanity: first LLM call saw the system prompt with skill + retrieved context.
	reqs := mock.Requests()
	if !strings.Contains(reqs[0].System, "Be careful") {
		t.Errorf("skill prompt missing")
	}
	if !strings.Contains(reqs[0].System, "arithmetic") {
		t.Errorf("retrieved doc missing")
	}
	// Tool was advertised.
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "calc" {
		t.Errorf("tool not advertised; got %+v", reqs[0].Tools)
	}
	// Second call saw the tool result.
	foundTool := false
	for _, m := range reqs[1].Messages {
		if m.Role == gantry.RoleTool && m.ToolCallID == "c1" && m.Content == "5" {
			foundTool = true
		}
	}
	if !foundTool {
		t.Errorf("tool result missing in second LLM call; messages: %+v", reqs[1].Messages)
	}
}
