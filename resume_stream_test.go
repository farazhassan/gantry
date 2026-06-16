package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestResumeStreamNilSinkErrors(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(respWith("x", 0, 0))))
	state, err := a.ResumeStream(context.Background(), gantry.NewState("go"), nil)
	if err == nil {
		t.Fatal("ResumeStream with nil sink should error")
	}
	if state == nil {
		t.Error("ResumeStream must return a non-nil state even on the nil-sink error")
	}
}

func TestResumeStreamNilPriorReturnsError(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(respWith("x", 0, 0))))
	got, err := a.ResumeStream(context.Background(), nil, func(gantry.Event) error { return nil })
	if err == nil {
		t.Error("ResumeStream(nil prior): want error, got nil")
	}
	if got == nil {
		t.Fatal("ResumeStream(nil prior): want non-nil State (Run-family contract), got nil")
	}
	if got.Input != "" || got.Done || got.DoneReason != "" {
		t.Errorf("ResumeStream(nil prior): want fresh empty State, got %+v", got)
	}
}

func TestResumeStreamTerminalIsNoOp(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("should not be used", 0, 0))
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))

	prior := gantry.NewState("done input")
	prior.Done = true
	prior.DoneReason = gantry.DoneNoToolCalls
	prior.FinalOutput = "already final"

	var events []gantry.Event
	got, err := a.ResumeStream(context.Background(), prior, func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("ResumeStream: %v", err)
	}
	if got != prior {
		t.Error("ResumeStream(terminal): returned a different pointer, want the same prior")
	}
	if len(events) != 0 {
		t.Errorf("terminal ResumeStream emitted %d events, want 0", len(events))
	}
	if len(llm.Requests()) != 0 {
		t.Errorf("LLM was called %d times on a terminal ResumeStream; want 0", len(llm.Requests()))
	}
}

func TestResumeStreamContinuesNonTerminalAndStreams(t *testing.T) {
	llm := eval.NewMockLLMClient(respWith("resumed answer", 4, 4))
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))

	prior := gantry.NewState("orig")
	prior.Messages = []gantry.Message{{Role: gantry.RoleUser, Content: "orig"}}
	prior.Done = false

	var sawDelta bool
	var events []gantry.Event
	got, err := a.ResumeStream(context.Background(), prior, func(ev gantry.Event) error {
		events = append(events, ev)
		if ev.Type == gantry.EventTextDelta {
			sawDelta = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ResumeStream: %v", err)
	}
	if got != prior {
		t.Error("ResumeStream: result should alias prior (runs in place)")
	}
	if !got.Done || got.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("Done=%v DoneReason=%q, want true / %q", got.Done, got.DoneReason, gantry.DoneNoToolCalls)
	}
	if got.FinalOutput != "resumed answer" {
		t.Errorf("FinalOutput = %q, want %q", got.FinalOutput, "resumed answer")
	}
	if !sawDelta {
		t.Error("expected at least one text_delta event from the streaming client")
	}
	if len(events) == 0 || events[len(events)-1].Type != gantry.EventDone {
		t.Error("expected a terminal done event")
	}
}
