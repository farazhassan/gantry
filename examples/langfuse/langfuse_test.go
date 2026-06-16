package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

// TestRunExampleWiring verifies the example wiring is sound without touching the
// network: it drives RunExample with the in-memory default tracer and confirms
// the agent runs to a clean stop. The real Langfuse wire contract is exercised
// by `go run ./examples/langfuse` against a live instance, not here.
func TestRunExampleWiring(t *testing.T) {
	tr := gantry.NewTrace()
	state, err := RunExample(context.Background(), gantry.NewDefaultTracer(tr))
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
	if state.FinalOutput == "" {
		t.Error("FinalOutput is empty, want the model's reply")
	}
	if len(tr.Snapshot()) == 0 {
		t.Error("tracer recorded no events, want at least the run span")
	}
}
