package harness_test

import (
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestDefaultPhases(t *testing.T) {
	phases := harness.DefaultPhases()
	want := []harness.Phase{
		harness.PhaseStart,
		harness.PhaseAssembleContext,
		harness.PhaseLLMCall,
		harness.PhasePostLLM,
		harness.PhaseToolExec,
		harness.PhaseObserve,
		harness.PhaseEnd,
	}
	if len(phases) != len(want) {
		t.Fatalf("DefaultPhases len = %d, want %d", len(phases), len(want))
	}
	for i, p := range want {
		if phases[i] != p {
			t.Errorf("phases[%d] = %q, want %q", i, phases[i], p)
		}
	}
}

func TestPhaseString(t *testing.T) {
	if string(harness.PhaseLLMCall) == "" {
		t.Errorf("PhaseLLMCall is empty string")
	}
}
