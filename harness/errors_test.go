package harness_test

import (
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestSentinelErrors(t *testing.T) {
	errs := []error{
		harness.ErrLLMTransient,
		harness.ErrLLMPermanent,
		harness.ErrToolExecution,
		harness.ErrGuardrailBlocked,
		harness.ErrLimitExceeded,
		harness.ErrHumanAborted,
		harness.ErrCheckpointFailed,
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
	cases := []harness.DoneReason{
		harness.DoneNoToolCalls,
		harness.DoneMaxIterations,
		harness.DoneBudgetExceeded,
		harness.DoneGuardrailBlocked,
		harness.DoneHumanAborted,
		harness.DoneError,
	}
	seen := map[harness.DoneReason]bool{}
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
