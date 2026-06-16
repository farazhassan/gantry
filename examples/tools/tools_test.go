package main

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestToolsRunComputesViaTool(t *testing.T) {
	state, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
	if !strings.Contains(state.FinalOutput, "5") {
		t.Errorf("FinalOutput = %q, want it to mention the result 5", state.FinalOutput)
	}
}
