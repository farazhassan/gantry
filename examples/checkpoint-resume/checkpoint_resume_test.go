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

func TestGracefulHandoffExample(t *testing.T) {
	res, err := RunGracefulHandoff(context.Background())
	if err != nil {
		t.Fatalf("RunGracefulHandoff: %v", err)
	}
	if res.FinalOutput != "final answer" {
		t.Errorf("FinalOutput = %q, want %q", res.FinalOutput, "final answer")
	}
}

func TestLiveWorkerBlocksTakeoverExample(t *testing.T) {
	res, err := RunLiveWorkerBlocksTakeover(context.Background())
	if err != nil {
		t.Fatalf("RunLiveWorkerBlocksTakeover: %v", err)
	}
	if !errors.Is(res.ConcurrentAttemptErr, checkpointer.ErrLeaseHeld) {
		t.Errorf("worker C concurrent attempt error = %v, want ErrLeaseHeld", res.ConcurrentAttemptErr)
	}
	if res.FinalOutput != "worker B's answer" {
		t.Errorf("FinalOutput = %q, want %q", res.FinalOutput, "worker B's answer")
	}
}

func TestLeaseLostCancelsRunExample(t *testing.T) {
	res, err := RunLeaseLostCancelsRun(context.Background())
	if err != nil {
		t.Fatalf("RunLeaseLostCancelsRun: %v", err)
	}
	if !errors.Is(res.RunErr, context.Canceled) {
		t.Errorf("RunErr = %v, want context.Canceled", res.RunErr)
	}
}
