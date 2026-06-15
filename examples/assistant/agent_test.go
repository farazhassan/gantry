package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

// TestBuildAgent_FullTurnWithToolCall scripts the LLM to call fs__read_file
// then answer, and asserts the turn completes and persists. The confirmer is
// fed "" (no prompts expected because read_file is read-only).
func TestBuildAgent_FullTurnWithToolCall(t *testing.T) {
	tools := newStubFSTools(t)

	llm := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: harness.LLMResponse{
			StopReason: harness.StopReasonToolUse,
			ToolCalls: []harness.ToolCall{
				{ID: "c1", Name: "fs__read_file", Input: json.RawMessage(`{"path":"/tmp/notes.txt"}`)},
			},
		}},
		{Response: harness.LLMResponse{
			StopReason: harness.StopReasonEnd,
			Content:    "Your notes say hello.",
		}},
	})

	confirmer := newCLIConfirmer(strings.NewReader(""), &strings.Builder{})
	askTool := ask.NewTool(ask.NewCLI(strings.NewReader(""), &strings.Builder{}))

	agent, err := buildAgent(buildConfig{
		LLM:       llm,
		Tools:     append(tools, askTool),
		Confirmer: confirmer,
	})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}

	mgr := session.NewManager(agent, checkpointer.NewInMemory())
	state, err := mgr.Session("t1").Run(context.Background(), "what do my notes say?")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(replyText(state), "hello") {
		t.Fatalf("want final output mentioning the tool result, got %q", replyText(state))
	}
}

// TestBuildAgent_DenyAbortsTurn scripts a mutating call and a confirmer that
// denies; the run must return an error (the turn aborts).
func TestBuildAgent_DenyAbortsTurn(t *testing.T) {
	tools := newStubFSTools(t)
	llm := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: harness.LLMResponse{
			StopReason: harness.StopReasonToolUse,
			ToolCalls:  []harness.ToolCall{{ID: "c1", Name: "fs__write_file", Input: json.RawMessage(`{"path":"/tmp/x","content":"hi"}`)}},
		}},
	})
	confirmer := newCLIConfirmer(strings.NewReader("n\n"), &strings.Builder{})

	agent, err := buildAgent(buildConfig{LLM: llm, Tools: tools, Confirmer: confirmer})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	mgr := session.NewManager(agent, checkpointer.NewInMemory())
	_, err = mgr.Session("t2").Run(context.Background(), "overwrite my file")
	if err == nil {
		t.Fatalf("expected an error from a denied turn")
	}
}

// TestBuildAgent_PersonaReachesModel proves the actual defaultPersona flows
// through the full middleware stack into the LLM request. The mock LLM records
// every request; the first request's System must equal the wired persona.
func TestBuildAgent_PersonaReachesModel(t *testing.T) {
	llm := eval.NewMockLLMClient(harness.LLMResponse{
		StopReason: harness.StopReasonEnd,
		Content:    "Hello.",
	})

	agent, err := buildAgent(buildConfig{
		LLM:          llm,
		Tools:        nil,
		Confirmer:    newCLIConfirmer(strings.NewReader(""), &strings.Builder{}),
		SystemPrompt: defaultPersona,
	})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}

	mgr := session.NewManager(agent, checkpointer.NewInMemory())
	if _, err := mgr.Session("persona").Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := llm.Requests()
	if len(reqs) == 0 {
		t.Fatalf("mock LLM recorded no requests")
	}
	if reqs[0].System != defaultPersona {
		t.Fatalf("want System %q to reach the model, got %q", defaultPersona, reqs[0].System)
	}
}
