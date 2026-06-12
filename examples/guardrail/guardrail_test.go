package main

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestGuardrailBlockReturnsSentinel(t *testing.T) {
	state, err := RunBlocked(context.Background())
	if !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Fatalf("error = %v, want errors.Is(err, ErrGuardrailBlocked)", err)
	}
	if state.DoneReason != harness.DoneGuardrailBlocked {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneGuardrailBlocked)
	}
}

func TestBudgetStopReturnsNilError(t *testing.T) {
	state, err := RunBudgetStop(context.Background())
	if err != nil {
		t.Fatalf("budget stop returned error %v, want nil (resource stops do not error)", err)
	}
	if state.DoneReason != harness.DoneBudgetExceeded {
		t.Errorf("DoneReason = %q, want %q", state.DoneReason, harness.DoneBudgetExceeded)
	}
}
