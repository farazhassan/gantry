package main

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

func newTestManager(t *testing.T, turns ...harness.LLMResponse) *session.Manager {
	t.Helper()
	script := make([]eval.MockTurn, len(turns))
	for i, r := range turns {
		script[i] = eval.MockTurn{Response: r}
	}
	llm := eval.NewMockLLMClientFromScript(script)
	agent, err := buildAgent(buildConfig{LLM: llm, Tools: nil, Confirmer: newCLIConfirmer(strings.NewReader(""), &strings.Builder{})})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	return session.NewManager(agent, checkpointer.NewInMemory())
}

func TestRunREPL_AnswersThenExits(t *testing.T) {
	mgr := newTestManager(t, harness.LLMResponse{StopReason: harness.StopReasonEnd, Content: "Hello there."})
	var out strings.Builder
	in := strings.NewReader("hi\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "Hello there.") {
		t.Fatalf("expected assistant reply in output, got: %q", out.String())
	}
}

func TestRunREPL_HelpCommand(t *testing.T) {
	mgr := newTestManager(t)
	var out strings.Builder
	in := strings.NewReader("/help\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "/reset") || !strings.Contains(out.String(), "/exit") {
		t.Fatalf("help should list commands, got: %q", out.String())
	}
}

func TestRunREPL_ResetSwitchesSession(t *testing.T) {
	mgr := newTestManager(t,
		harness.LLMResponse{StopReason: harness.StopReasonEnd, Content: "first answer"},
		harness.LLMResponse{StopReason: harness.StopReasonEnd, Content: "second answer"},
	)
	var out strings.Builder
	in := strings.NewReader("question one\n/reset\nquestion two\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "first answer") || !strings.Contains(got, "second answer") {
		t.Fatalf("both turns should answer across reset, got: %q", got)
	}
	if !strings.Contains(got, "new session") {
		t.Fatalf("reset should announce a new session, got: %q", got)
	}
}

func TestRunREPL_EmptyLineIgnored(t *testing.T) {
	mgr := newTestManager(t, harness.LLMResponse{StopReason: harness.StopReasonEnd, Content: "answer"})
	var out strings.Builder
	in := strings.NewReader("\n   \nreal question\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "answer") {
		t.Fatalf("expected the non-empty line to be processed, got: %q", out.String())
	}
}
