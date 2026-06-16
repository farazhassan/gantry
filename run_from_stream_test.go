package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestRunFromStreamNilSinkErrors(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	state, err := a.RunFromStream(context.Background(), nil, "go", nil)
	if err == nil {
		t.Fatal("RunFromStream with nil sink should error")
	}
	if state == nil {
		t.Error("RunFromStream must return a non-nil state even on the nil-sink error")
	}
}

func TestRunFromStreamNilPriorMatchesRunStream(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	var events []gantry.Event
	state, err := a.RunFromStream(context.Background(), nil, "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	})
	if err != nil {
		t.Fatalf("RunFromStream: %v", err)
	}
	if state.FinalOutput != "the answer is 5" {
		t.Errorf("FinalOutput = %q, want \"the answer is 5\"", state.FinalOutput)
	}
	if len(events) == 0 || events[len(events)-1].Type != gantry.EventDone {
		t.Error("expected a terminal done event")
	}
}

func TestRunFromStreamCarriesPriorAndStreams(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(twoTurnMock()))
	fakeToolExec(a)

	prior := &gantry.State{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "earlier question"},
			{Role: gantry.RoleAssistant, Content: "earlier answer"},
		},
	}

	var sawDelta bool
	state, err := a.RunFromStream(context.Background(), prior, "follow up", func(ev gantry.Event) error {
		if ev.Type == gantry.EventTextDelta {
			sawDelta = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunFromStream: %v", err)
	}
	// prior (2) + new user message (1) + assistant turns (tool-call turn + final) => continuity preserved.
	if len(state.Messages) < 3 || state.Messages[0].Content != "earlier question" {
		t.Errorf("prior transcript not carried: got %d messages, first=%q", len(state.Messages), state.Messages[0].Content)
	}
	if !sawDelta {
		t.Error("expected at least one text_delta event from the streaming client")
	}
}
