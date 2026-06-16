package gantry_test

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestDefaultPhases(t *testing.T) {
	phases := gantry.DefaultPhases()
	want := []gantry.Phase{
		gantry.PhaseStart,
		gantry.PhaseAssembleContext,
		gantry.PhaseLLMCall,
		gantry.PhasePostLLM,
		gantry.PhaseToolExec,
		gantry.PhaseObserve,
		gantry.PhaseEnd,
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
	if string(gantry.PhaseLLMCall) == "" {
		t.Errorf("PhaseLLMCall is empty string")
	}
}
