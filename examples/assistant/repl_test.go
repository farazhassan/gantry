package main

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/session"
)

func newTestManager(t *testing.T, turns ...gantry.LLMResponse) *session.Manager {
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
	mgr := newTestManager(t, gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "Hello there."})
	var out strings.Builder
	in := strings.NewReader("hi\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out, nil); err != nil {
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
	if err := runREPL(context.Background(), mgr, "default", in, &out, nil); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "/reset") || !strings.Contains(out.String(), "/exit") {
		t.Fatalf("help should list commands, got: %q", out.String())
	}
}

func TestRunREPL_ResetSwitchesSession(t *testing.T) {
	mgr := newTestManager(t,
		gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "first answer"},
		gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "second answer"},
	)
	var out strings.Builder
	in := strings.NewReader("question one\n/reset\nquestion two\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out, nil); err != nil {
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

// TestRunREPL_SharedReaderFeedsConfirmer is a regression test for a stdin
// starvation bug: the REPL and the confirmer must read from the same buffered
// reader. When they don't, the REPL's reader reads ahead and swallows the
// confirmer's y/N line, so every mutating action is denied under piped input.
//
// This mirrors main.go's wiring: one *bufio.Reader is shared between the REPL
// and the confirmer (bufio.NewReader returns the same reader when handed one).
// The input "overwrite my file\ny\n/exit\n" must let the confirmer read "y"
// and approve the write — the output must contain the success reply and must
// NOT contain the deny message.
func TestRunREPL_SharedReaderFeedsConfirmer(t *testing.T) {
	tools := newStubFSTools(t)
	llm := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: gantry.LLMResponse{
			StopReason: gantry.StopReasonToolUse,
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "fs__write_file", Input: json.RawMessage(`{"path":"/tmp/x","content":"hi"}`)}},
		}},
		{Response: gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "Done writing."}},
	})

	shared := bufio.NewReader(strings.NewReader("overwrite my file\ny\n/exit\n"))
	var out strings.Builder

	agent, err := buildAgent(buildConfig{
		LLM:       llm,
		Tools:     tools,
		Confirmer: newCLIConfirmer(shared, &out),
	})
	if err != nil {
		t.Fatalf("buildAgent: %v", err)
	}
	mgr := session.NewManager(agent, checkpointer.NewInMemory())

	if err := runREPL(context.Background(), mgr, "shared", shared, &out, nil); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "action denied") {
		t.Fatalf("confirmer was starved of its y/N line (action denied), got: %q", got)
	}
	if !strings.Contains(got, "Done writing.") {
		t.Fatalf("expected approved write to complete the turn, got: %q", got)
	}
}

// TestRunREPL_InterruptCancelsOnlyCurrentTurn is a regression test for a bug
// where every turn shared one signal-derived context: once the first Ctrl-C
// cancelled it, the context stayed cancelled and aborted every later turn.
// The fix scopes cancellation to each turn. Here a fake armInterrupt cancels
// the FIRST turn (simulating Ctrl-C) and is a no-op for the rest; the first
// turn must report cancellation while the second still completes normally.
func TestRunREPL_InterruptCancelsOnlyCurrentTurn(t *testing.T) {
	// One scripted response. The cancelled first turn returns before any LLM
	// call, so it consumes nothing; the surviving second turn consumes this
	// response. The point is that the second turn runs at all.
	mgr := newTestManager(t,
		gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "surviving answer"},
	)
	var out strings.Builder
	in := strings.NewReader("question one\nquestion two\n/exit\n")

	turn := 0
	arm := func(cancel context.CancelFunc) func() {
		turn++
		if turn == 1 {
			cancel() // simulate Ctrl-C during the first turn only
		}
		return func() {}
	}

	if err := runREPL(context.Background(), mgr, "default", in, &out, arm); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "(turn cancelled)") {
		t.Fatalf("first turn should report cancellation, got: %q", got)
	}
	// The surviving answer must appear AFTER the cancellation notice, proving
	// the second turn ran on a fresh, uncancelled context.
	cancelAt := strings.Index(got, "(turn cancelled)")
	answerAt := strings.Index(got, "surviving answer")
	if answerAt < 0 || answerAt < cancelAt {
		t.Fatalf("second turn must run normally after the first was cancelled, got: %q", got)
	}
}

func TestRunREPL_EmptyLineIgnored(t *testing.T) {
	mgr := newTestManager(t, gantry.LLMResponse{StopReason: gantry.StopReasonEnd, Content: "answer"})
	var out strings.Builder
	in := strings.NewReader("\n   \nreal question\n/exit\n")
	if err := runREPL(context.Background(), mgr, "default", in, &out, nil); err != nil {
		t.Fatalf("runREPL: %v", err)
	}
	if !strings.Contains(out.String(), "answer") {
		t.Fatalf("expected the non-empty line to be processed, got: %q", out.String())
	}
}
