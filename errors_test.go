package gantry_test

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestSentinelErrors(t *testing.T) {
	errs := []error{
		gantry.ErrLLMTransient,
		gantry.ErrLLMPermanent,
		gantry.ErrToolExecution,
		gantry.ErrGuardrailBlocked,
		gantry.ErrLimitExceeded,
		gantry.ErrHumanAborted,
		gantry.ErrCheckpointFailed,
	}
	for _, e := range errs {
		if e == nil {
			t.Errorf("sentinel error is nil")
		}
		if e.Error() == "" {
			t.Errorf("sentinel %v has empty message", e)
		}
	}
}

func TestDoneReasonConstants(t *testing.T) {
	cases := []gantry.DoneReason{
		gantry.DoneNoToolCalls,
		gantry.DoneMaxIterations,
		gantry.DoneBudgetExceeded,
		gantry.DoneGuardrailBlocked,
		gantry.DoneHumanAborted,
		gantry.DoneError,
		gantry.DoneClientToolCall,
	}
	seen := map[gantry.DoneReason]bool{}
	for _, c := range cases {
		if c == "" {
			t.Errorf("DoneReason value is empty")
		}
		if seen[c] {
			t.Errorf("DoneReason %q is duplicated", c)
		}
		seen[c] = true
	}
}
