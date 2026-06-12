package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestMinimalRunStopsWithNoToolCalls(t *testing.T) {
	state, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if state.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneNoToolCalls)
	}
	if state.FinalOutput == "" {
		t.Error("FinalOutput is empty, want the model's reply")
	}
}
