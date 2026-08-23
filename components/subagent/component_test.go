package subagent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/eval"
)

func TestComponentFoldsChildUsage(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{
		Content:    "child answer",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 7, OutputTokens: 11},
	})
	delegate := New("specialist", "answers as a specialist", child)

	parentLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"answer"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 1, OutputTokens: 2},
		},
		gantry.LLMResponse{
			Content:    "done",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 3, OutputTokens: 4},
		},
	)
	parent, err := gantry.NewAgent(
		gantry.WithLLM(parentLLM),
		gantry.WithComponents(Component(1, delegate)),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	st, err := parent.Run(context.Background(), "ask the specialist")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 11, OutputTokens: 17} // own 4/6 + child 7/11
	if st.Usage != want {
		t.Errorf("Usage = %+v, want %+v", st.Usage, want)
	}
}

func TestPlainFromToolsDoesNotFoldChildUsage(t *testing.T) {
	// Regression pin: without Component's fold middleware, child usage is
	// invisible to the parent — exactly the gap Component closes.
	child := newChildAgent(t, gantry.LLMResponse{
		Content:    "child answer",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 7, OutputTokens: 11},
	})
	delegate := New("specialist", "answers as a specialist", child)

	parentLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"answer"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 1, OutputTokens: 2},
		},
		gantry.LLMResponse{
			Content:    "done",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 3, OutputTokens: 4},
		},
	)
	parent, err := gantry.NewAgent(
		gantry.WithLLM(parentLLM),
		gantry.WithComponents(tool.FromTools(1, delegate)),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	st, err := parent.Run(context.Background(), "ask the specialist")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 4, OutputTokens: 6} // parent's own only
	if st.Usage != want {
		t.Errorf("Usage = %+v, want %+v (no fold without Component)", st.Usage, want)
	}
}

func TestNestedDelegatesFoldOnceAtEachLevel(t *testing.T) {
	grandchild := newChildAgent(t, gantry.LLMResponse{
		Content:    "grand answer",
		StopReason: gantry.StopReasonEnd,
		Usage:      gantry.Usage{InputTokens: 5, OutputTokens: 5},
	})

	childLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "g1", Name: "deep_specialist", Input: json.RawMessage(`{"goal":"dig"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 2, OutputTokens: 2},
		},
		gantry.LLMResponse{
			Content:    "child answer",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 2, OutputTokens: 2},
		},
	)
	child, err := gantry.NewAgent(
		gantry.WithLLM(childLLM),
		gantry.WithComponents(Component(1, New("deep_specialist", "digs deeper", grandchild))),
	)
	if err != nil {
		t.Fatalf("NewAgent(child): %v", err)
	}

	parentLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"answer"}`)}},
			StopReason: gantry.StopReasonToolUse,
			Usage:      gantry.Usage{InputTokens: 1, OutputTokens: 1},
		},
		gantry.LLMResponse{
			Content:    "done",
			StopReason: gantry.StopReasonEnd,
			Usage:      gantry.Usage{InputTokens: 1, OutputTokens: 1},
		},
	)
	parent, err := gantry.NewAgent(
		gantry.WithLLM(parentLLM),
		gantry.WithComponents(Component(1, New("specialist", "answers as a specialist", child))),
	)
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}

	st, err := parent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// parent own 2/2 + child own 4/4 + grandchild 5/5, each counted exactly once.
	want := gantry.Usage{InputTokens: 11, OutputTokens: 11}
	if st.Usage != want {
		t.Errorf("Usage = %+v, want %+v (no double counting across levels)", st.Usage, want)
	}
}

func TestComponentWithRegistryReturnsUsableRegistry(t *testing.T) {
	child := newChildAgent(t, gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	delegate := New("specialist", "d", child)

	comp, reg := ComponentWithRegistry(1, delegate)
	if reg == nil {
		t.Fatal("ComponentWithRegistry returned a nil *tool.Registry")
	}
	got, ok := reg.Lookup("specialist")
	if !ok || got.Definition().Name != "specialist" {
		t.Errorf("reg.Lookup(specialist) = (%#v, %v), want the delegate tool", got, ok)
	}

	// The returned Component must still install and dispatch exactly like
	// Component — this is not a second, disconnected registry.
	parentLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "specialist", Input: json.RawMessage(`{"goal":"g"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd},
	)
	parent, err := gantry.NewAgent(gantry.WithLLM(parentLLM), gantry.WithComponents(comp))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if _, err := parent.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(parentLLM.Requests()) != 2 {
		t.Fatalf("requests = %d, want 2 (dispatch ran normally)", len(parentLLM.Requests()))
	}
}

func TestComponentInstallTwiceErrors(t *testing.T) {
	child := newChildAgent(t)
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(Component(1, New("s", "d", child))); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := a.With(Component(1, New("s", "d", child))); err == nil {
		t.Errorf("second install = nil error, want tool-dispatch-already-installed error")
	}
}
