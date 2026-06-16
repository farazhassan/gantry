package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestRAGRetrievesDocuments(t *testing.T) {
	state, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if len(state.Retrieved) != 2 {
		t.Errorf("len(state.Retrieved) = %d, want 2 (k=2 truncates the 3 docs)", len(state.Retrieved))
	}
	if state.DoneReason != gantry.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, gantry.DoneNoToolCalls)
	}
}
