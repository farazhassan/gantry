package session_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/session"
)

func TestSessionRunStreamStreamsAndPersists(t *testing.T) {
	a := newTestAgent(t, resp("hello there friend", 10, 5))
	mgr := session.NewManager(a, checkpointer.NewInMemory())
	s := mgr.Session("user-1")
	ctx := context.Background()

	var deltas []string
	var sawDone bool
	state, err := s.RunStream(ctx, "hi", func(ev gantry.Event) error {
		switch ev.Type {
		case gantry.EventTextDelta:
			deltas = append(deltas, ev.TextDelta)
		case gantry.EventDone:
			sawDone = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !sawDone {
		t.Error("expected a terminal done event")
	}
	// Concatenated deltas reconstruct the model's content exactly.
	if got := strings.Join(deltas, ""); got != "hello there friend" {
		t.Errorf("streamed text = %q, want %q", got, "hello there friend")
	}
	if state.FinalOutput != "hello there friend" {
		t.Errorf("FinalOutput = %q", state.FinalOutput)
	}

	// Persisted exactly like Run: a follow-up turn carries the transcript.
	h, err := s.History(ctx)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h) != 2 {
		t.Errorf("History after streamed turn = %d messages, want 2", len(h))
	}
}

func TestSessionRunStreamContinuesPriorRun(t *testing.T) {
	a := newTestAgent(t, resp("first answer", 10, 5), resp("second answer", 10, 5))
	mgr := session.NewManager(a, checkpointer.NewInMemory())
	s := mgr.Session("user-2")
	ctx := context.Background()

	if _, err := s.Run(ctx, "first"); err != nil {
		t.Fatalf("turn 1 (Run): %v", err)
	}

	t2, err := s.RunStream(ctx, "second", func(gantry.Event) error { return nil })
	if err != nil {
		t.Fatalf("turn 2 (RunStream): %v", err)
	}
	if len(t2.Messages) != 4 {
		t.Errorf("streamed turn 2 len(Messages) = %d, want 4 (prior transcript carried)", len(t2.Messages))
	}
	if t2.Usage.InputTokens != 20 {
		t.Errorf("streamed turn 2 Usage.InputTokens = %d, want 20 (cumulative)", t2.Usage.InputTokens)
	}
}
