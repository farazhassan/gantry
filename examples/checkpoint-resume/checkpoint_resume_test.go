package main

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
)

func TestCheckpointResumeExample(t *testing.T) {
	res, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample: %v", err)
	}
	if !errors.Is(res.WorkerBFirstAttemptErr, checkpointer.ErrLeaseHeld) {
		t.Errorf("worker B first attempt error = %v, want ErrLeaseHeld", res.WorkerBFirstAttemptErr)
	}
	if res.FinalOutput != "final answer" {
		t.Errorf("FinalOutput = %q, want %q", res.FinalOutput, "final answer")
	}
}
