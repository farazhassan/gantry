package session_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/session"
)

// withAlwaysHandoff installs a test middleware that terminates EVERY run of
// agent a with a transfer handoff to target — the same seam a real routing
// middleware sets (see examples/handoff). It runs at PhasePostLLM after the
// default handler, exactly where routing middleware lives.
func withAlwaysHandoff(a *gantry.Agent, target string) {
	a.Use(gantry.PhasePostLLM, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			s.Handoff = &gantry.Handoff{Target: target, Mode: gantry.HandoffTransfer, Reason: "test route"}
			s.Done = true
			s.DoneReason = gantry.DoneHandoff
			return nil
		}
	})
}

func TestSessionTransferHandoffRunsTargetAgent(t *testing.T) {
	router := newTestAgent(t, resp("routing you to billing", 10, 5))
	withAlwaysHandoff(router, "billing")
	billing := newTestAgent(t, resp("your invoice is fixed", 7, 3))

	mgr := session.NewManager(router, checkpointer.NewInMemory(),
		session.WithResolver(func(sessionID string, h *gantry.Handoff) *gantry.Agent {
			if h.Target == "billing" {
				return billing
			}
			return nil
		}))
	s := mgr.Session("user-1")

	state, err := s.Run(context.Background(), "my invoice is wrong")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q (the billing agent finished the turn)",
			state.DoneReason, gantry.DoneNoToolCalls)
	}
	if state.FinalOutput != "your invoice is fixed" {
		t.Errorf("FinalOutput = %q, want the billing agent's answer", state.FinalOutput)
	}
	// One accumulated transcript: user + router assistant + billing assistant.
	if len(state.Messages) != 3 {
		t.Errorf("len(Messages) = %d, want 3", len(state.Messages))
	}
	// Usage accumulates across the hop (router 10/5 + billing 7/3).
	if state.Usage.InputTokens != 17 || state.Usage.OutputTokens != 8 {
		t.Errorf("Usage = %+v, want 17 in / 8 out", state.Usage)
	}
	// The routed turn persisted (next-turn continuity).
	h, err := s.History(context.Background())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(h) != 3 {
		t.Errorf("persisted history = %d messages, want 3", len(h))
	}
}

func TestSessionHandoffWithoutResolverIsReturnedAsIs(t *testing.T) {
	router := newTestAgent(t, resp("routing you", 1, 1))
	withAlwaysHandoff(router, "billing")
	mgr := session.NewManager(router, checkpointer.NewInMemory())
	s := mgr.Session("user-1")

	state, err := s.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.DoneReason != gantry.DoneHandoff {
		t.Errorf("DoneReason = %q, want %q (nil resolver keeps current behavior)",
			state.DoneReason, gantry.DoneHandoff)
	}
	if state.Handoff == nil || state.Handoff.Target != "billing" {
		t.Errorf("Handoff = %+v, want target billing", state.Handoff)
	}
	// Current behavior persists the terminal state like any other turn.
	h, _ := s.History(context.Background())
	if len(h) != 2 {
		t.Errorf("persisted history = %d messages, want 2", len(h))
	}
}

func TestSessionHandoffUnknownTargetErrors(t *testing.T) {
	router := newTestAgent(t, resp("routing you", 1, 1))
	withAlwaysHandoff(router, "nonexistent")
	mgr := session.NewManager(router, checkpointer.NewInMemory(),
		session.WithResolver(func(string, *gantry.Handoff) *gantry.Agent { return nil }))
	s := mgr.Session("user-1")

	state, err := s.Run(context.Background(), "hello")
	if !errors.Is(err, session.ErrHandoffTargetUnknown) {
		t.Fatalf("err = %v, want errors.Is(..., ErrHandoffTargetUnknown)", err)
	}
	if state == nil || state.DoneReason != gantry.DoneHandoff {
		t.Errorf("state = %+v, want the DoneHandoff state alongside the error", state)
	}
	// The failed turn is NOT persisted.
	h, _ := s.History(context.Background())
	if len(h) != 0 {
		t.Errorf("persisted history = %d messages, want 0 (turn not saved)", len(h))
	}
}

func TestSessionHandoffLoopIsBounded(t *testing.T) {
	// One agent that always hands off to itself: the initial run plus the 3
	// allowed hops consume 4 scripted responses; the 4th pending handoff errors.
	looper := newTestAgent(t,
		resp("hop 0", 1, 1), resp("hop 1", 1, 1), resp("hop 2", 1, 1), resp("hop 3", 1, 1))
	withAlwaysHandoff(looper, "self")
	mgr := session.NewManager(looper, checkpointer.NewInMemory(),
		session.WithResolver(func(string, *gantry.Handoff) *gantry.Agent { return looper }))
	s := mgr.Session("user-1")

	state, err := s.Run(context.Background(), "hello")
	if !errors.Is(err, session.ErrHandoffLoop) {
		t.Fatalf("err = %v, want errors.Is(..., ErrHandoffLoop)", err)
	}
	if state == nil || state.DoneReason != gantry.DoneHandoff {
		t.Errorf("state = %+v, want the last DoneHandoff state alongside the error", state)
	}
	h, _ := s.History(context.Background())
	if len(h) != 0 {
		t.Errorf("persisted history = %d messages, want 0 (turn not saved)", len(h))
	}
}

func TestSessionDelegateHandoffFallsThrough(t *testing.T) {
	a := newTestAgent(t, resp("delegating", 1, 1))
	a.Use(gantry.PhasePostLLM, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			s.Handoff = &gantry.Handoff{Target: "worker", Mode: gantry.HandoffDelegate, Reason: "test"}
			s.Done = true
			s.DoneReason = gantry.DoneHandoff
			return nil
		}
	})
	resolved := false
	mgr := session.NewManager(a, checkpointer.NewInMemory(),
		session.WithResolver(func(string, *gantry.Handoff) *gantry.Agent {
			resolved = true
			return a
		}))

	state, err := mgr.Session("user-1").Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolved {
		t.Error("resolver ran for a delegate handoff; delegation is plan 06's scope")
	}
	if state.DoneReason != gantry.DoneHandoff || state.Handoff == nil || state.Handoff.Mode != gantry.HandoffDelegate {
		t.Errorf("state = %q/%+v, want the delegate handoff returned to the caller as-is",
			state.DoneReason, state.Handoff)
	}
}

func TestSessionRunStreamHandoffStreamsBothRuns(t *testing.T) {
	router := newTestAgent(t, resp("routing you to billing", 10, 5))
	withAlwaysHandoff(router, "billing")
	billing := newTestAgent(t, resp("your invoice is fixed", 7, 3))
	mgr := session.NewManager(router, checkpointer.NewInMemory(),
		session.WithResolver(func(_ string, h *gantry.Handoff) *gantry.Agent { return billing }))
	s := mgr.Session("user-1")

	var doneEvents int
	state, err := s.RunStream(context.Background(), "my invoice is wrong", func(ev gantry.Event) error {
		if ev.Type == gantry.EventDone {
			doneEvents++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if state.FinalOutput != "your invoice is fixed" {
		t.Errorf("FinalOutput = %q, want the billing answer", state.FinalOutput)
	}
	// Each hop is a full agent run, so the sink sees one done event per run:
	// the router's (DoneHandoff) and the billing agent's (DoneNoToolCalls).
	if doneEvents != 2 {
		t.Errorf("done events = %d, want 2 (one per hop)", doneEvents)
	}
}
