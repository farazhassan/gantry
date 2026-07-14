package gantry_test

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestHandoffStateCarriesMessagesUsageAndPublicMeta(t *testing.T) {
	prior := &gantry.State{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "my invoice is wrong"},
			{Role: gantry.RoleAssistant, Content: "routing you to billing"},
		},
		Usage:       gantry.Usage{InputTokens: 10, OutputTokens: 5, Cost: 0.01},
		Done:        true,
		DoneReason:  gantry.DoneHandoff,
		Handoff:     &gantry.Handoff{Target: "billing", Mode: gantry.HandoffTransfer},
		FinalOutput: "routing you to billing",
		Iteration:   4,
		Meta: map[string]any{
			"task.id":                      "tk-1",
			"components/tool:client_tools": map[string]bool{"ask_user": true},
		},
	}

	hs := gantry.HandoffState(prior)

	if len(hs.Messages) != 2 || hs.Messages[0].Content != "my invoice is wrong" {
		t.Errorf("Messages not carried: %+v", hs.Messages)
	}
	if hs.Usage != prior.Usage {
		t.Errorf("Usage = %+v, want %+v (cumulative across the hop)", hs.Usage, prior.Usage)
	}
	if hs.Meta["task.id"] != "tk-1" {
		t.Errorf("public meta key stripped: %v", hs.Meta)
	}
	if _, ok := hs.Meta["components/tool:client_tools"]; ok {
		t.Error("component-private meta key (contains \"/\") must be stripped")
	}
}

func TestHandoffStateIsNonTerminal(t *testing.T) {
	prior := &gantry.State{
		Messages:         []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
		Done:             true,
		DoneReason:       gantry.DoneHandoff,
		Handoff:          &gantry.Handoff{Target: "x", Mode: gantry.HandoffTransfer},
		FinalOutput:      "routing",
		PendingToolCalls: []gantry.ToolCall{{ID: "c1", Name: "handoff"}},
		Iteration:        7,
	}
	hs := gantry.HandoffState(prior)
	if hs.Done || hs.DoneReason != "" || hs.Handoff != nil || hs.FinalOutput != "" {
		t.Errorf("termination not cleared: Done=%v DoneReason=%q Handoff=%v FinalOutput=%q",
			hs.Done, hs.DoneReason, hs.Handoff, hs.FinalOutput)
	}
	if len(hs.PendingToolCalls) != 0 {
		t.Errorf("PendingToolCalls = %v, want empty (per-run scratch)", hs.PendingToolCalls)
	}
	if hs.Iteration != 0 {
		t.Errorf("Iteration = %d, want 0 (the target gets a fresh loop budget)", hs.Iteration)
	}
	if hs.Trace == nil {
		t.Error("Trace is nil, want a fresh trace (matches newStateFrom)")
	}
}

func TestHandoffStateCopiesDoNotAliasPrior(t *testing.T) {
	prior := &gantry.State{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
		Meta:     map[string]any{"k": "v"},
	}
	hs := gantry.HandoffState(prior)
	hs.Messages[0].Content = "mutated"
	hs.Messages = append(hs.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "extra"})
	hs.Meta["k"] = "changed"

	if prior.Messages[0].Content != "hi" || len(prior.Messages) != 1 {
		t.Errorf("prior.Messages mutated through the handoff copy: %+v", prior.Messages)
	}
	if prior.Meta["k"] != "v" {
		t.Errorf("prior.Meta mutated through the handoff copy: %v", prior.Meta)
	}
}

func TestHandoffStateNilPriorReturnsFreshState(t *testing.T) {
	hs := gantry.HandoffState(nil)
	if hs == nil {
		t.Fatal("HandoffState(nil) = nil, want a non-nil empty State (Run-family contract)")
	}
	if hs.Done || len(hs.Messages) != 0 {
		t.Errorf("HandoffState(nil) = %+v, want a fresh empty state", hs)
	}
}
