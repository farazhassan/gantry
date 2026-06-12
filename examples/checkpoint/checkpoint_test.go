package main

import (
	"context"
	"testing"
)

func TestCheckpointRoundTrip(t *testing.T) {
	res, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}
	if res.Loaded == nil {
		t.Fatal("loaded checkpoint is nil")
	}
	if res.Loaded.Input != res.Live.Input {
		t.Errorf("loaded Input = %q, want %q", res.Loaded.Input, res.Live.Input)
	}
	if res.Loaded.FinalOutput != res.Live.FinalOutput {
		t.Errorf("loaded FinalOutput = %q, want %q", res.Loaded.FinalOutput, res.Live.FinalOutput)
	}
	if res.Loaded.FinalOutput == "" {
		t.Error("loaded FinalOutput is empty, want the saved answer")
	}
}
