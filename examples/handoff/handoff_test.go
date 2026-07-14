package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestHandoffExampleRoutesToBilling(t *testing.T) {
	state, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q (the billing agent finished the turn)",
			state.DoneReason, gantry.DoneNoToolCalls)
	}
	if want := "I checked your invoice: the duplicate charge has been refunded."; state.FinalOutput != want {
		t.Errorf("FinalOutput = %q, want %q", state.FinalOutput, want)
	}
	// The routed transcript carries the router's classification AND the billing
	// answer: user, assistant(handoff call), tool(fulfillment), assistant(answer).
	if len(state.Messages) != 4 {
		t.Errorf("len(Messages) = %d, want 4", len(state.Messages))
	}
	if state.Messages[2].Role != gantry.RoleTool || state.Messages[2].ToolCallID != "call-1" {
		t.Errorf("Messages[2] = %+v, want the tool-role fulfillment of the handoff call", state.Messages[2])
	}
}
