package main

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestSubagentExampleDelegatesAndFoldsUsage(t *testing.T) {
	state, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
	if !strings.Contains(state.FinalOutput, "context package") {
		t.Errorf("FinalOutput = %q, want the writer's post", state.FinalOutput)
	}
	if state.Usage.InputTokens != 216 || state.Usage.OutputTokens != 118 {
		t.Errorf("Usage = %+v, want 216 in / 118 out (coordinator 36/18 + researcher 100/40 + writer 80/60)", state.Usage)
	}
}
