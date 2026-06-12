package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestMiddlewareRetriesThenSucceeds(t *testing.T) {
	res, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if res.Attempts < 2 {
		t.Errorf("LLM attempts = %d, want >= 2 (retry should have re-invoked the call)", res.Attempts)
	}
	// logging sits innermost, so it records once per LLM attempt.
	if len(res.Logged) != res.Attempts {
		t.Errorf("logged %d calls, want one per attempt (%d)", len(res.Logged), res.Attempts)
	}
	if res.State.FinalOutput == "" {
		t.Error("FinalOutput is empty, want the recovered reply")
	}
	if res.State.DoneReason != harness.DoneNoToolCalls {
		t.Errorf("DoneReason = %q, want %q", res.State.DoneReason, harness.DoneNoToolCalls)
	}
}
