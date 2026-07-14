package gantry_test

import (
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestDoneHandoffConstant(t *testing.T) {
	if got := string(gantry.DoneHandoff); got != "handoff" {
		t.Errorf("DoneHandoff = %q, want \"handoff\"", got)
	}
}

func TestHandoffModeConstants(t *testing.T) {
	if got := string(gantry.HandoffTransfer); got != "transfer" {
		t.Errorf("HandoffTransfer = %q, want \"transfer\"", got)
	}
	if got := string(gantry.HandoffDelegate); got != "delegate" {
		t.Errorf("HandoffDelegate = %q, want \"delegate\"", got)
	}
}

// State has no JSON tags, so the new field must survive the checkpointer's
// Marshal->Unmarshal path (see components/checkpointer/state_roundtrip_test.go
// for the whole-State fixture; this covers the new field specifically).
func TestStateHandoffJSONRoundTrip(t *testing.T) {
	orig := &gantry.State{
		Done:       true,
		DoneReason: gantry.DoneHandoff,
		Handoff:    &gantry.Handoff{Target: "billing", Mode: gantry.HandoffTransfer, Reason: "invoice question"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got gantry.State
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Handoff == nil {
		t.Fatal("Handoff lost in JSON round-trip")
	}
	want := gantry.Handoff{Target: "billing", Mode: gantry.HandoffTransfer, Reason: "invoice question"}
	if *got.Handoff != want {
		t.Errorf("Handoff = %+v, want %+v", *got.Handoff, want)
	}
}
