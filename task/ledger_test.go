package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func newLedgerTask() *Task {
	return &Task{
		ID: "tk-1",
		Plan: &gantry.Plan{
			Goal: "ship it",
			Steps: []gantry.PlanStep{
				{ID: "s1", Description: "design", Status: gantry.StepDone, AcceptanceCriteria: "spec approved", Output: "spec.md"},
				{ID: "s2", Description: "build", Status: gantry.StepActive, AcceptanceCriteria: "tests pass"},
				{ID: "s3", Description: "ship", Status: gantry.StepPending, AcceptanceCriteria: "deployed"},
			},
		},
	}
}

func TestHydrateIsIndependentDeepCopy(t *testing.T) {
	tk := newLedgerTask()
	proj := Hydrate(tk)
	if proj == nil || proj == tk.Plan {
		t.Fatal("Hydrate must return a non-nil, distinct *Plan")
	}
	if proj.Goal != "ship it" || len(proj.Steps) != 3 {
		t.Fatalf("projection mismatch: %+v", proj)
	}
	// Mutating the projection must not touch the ledger.
	proj.Steps[1].Status = gantry.StepDone
	proj.Steps[1].Output = "binary"
	if tk.Plan.Steps[1].Status != gantry.StepActive || tk.Plan.Steps[1].Output != "" {
		t.Errorf("ledger mutated through projection: %+v", tk.Plan.Steps[1])
	}
}

func TestHydrateNilPlan(t *testing.T) {
	if got := Hydrate(&Task{ID: "x"}); got != nil {
		t.Errorf("Hydrate with nil ledger plan = %+v, want nil", got)
	}
}

func TestFlushReconcilesByID(t *testing.T) {
	tk := newLedgerTask()
	proj := Hydrate(tk)
	// Simulate a run: s2 finished, s3 started.
	proj.Steps[1].Status = gantry.StepDone
	proj.Steps[1].Output = "binary built"
	proj.Steps[2].Status = gantry.StepActive

	Flush(tk, proj)

	if tk.Plan.Steps[1].Status != gantry.StepDone || tk.Plan.Steps[1].Output != "binary built" {
		t.Errorf("s2 not reconciled: %+v", tk.Plan.Steps[1])
	}
	if tk.Plan.Steps[2].Status != gantry.StepActive {
		t.Errorf("s3 not reconciled: %+v", tk.Plan.Steps[2])
	}
	// Untouched fields preserved.
	if tk.Plan.Steps[0].Status != gantry.StepDone || tk.Plan.Steps[0].Description != "design" {
		t.Errorf("s1 clobbered: %+v", tk.Plan.Steps[0])
	}
}

func TestFlushIgnoresUnknownAndMissingIDs(t *testing.T) {
	tk := newLedgerTask()
	// A projection that dropped a step and added an unknown one must not panic
	// or corrupt the ledger; only matching IDs are reconciled.
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s2", Status: gantry.StepDone},
		{ID: "ghost", Status: gantry.StepDone},
	}}
	Flush(tk, proj)
	if tk.Plan.Steps[1].Status != gantry.StepDone {
		t.Errorf("s2 not updated: %+v", tk.Plan.Steps[1])
	}
	if len(tk.Plan.Steps) != 3 {
		t.Errorf("Flush changed step count to %d", len(tk.Plan.Steps))
	}
	// Steps not present in the projection must be left entirely unchanged.
	if tk.Plan.Steps[0].Status != gantry.StepDone || tk.Plan.Steps[2].Status != gantry.StepPending {
		t.Errorf("unmatched steps were mutated: %+v / %+v", tk.Plan.Steps[0], tk.Plan.Steps[2])
	}
}

func TestFlushNilSafe(t *testing.T) {
	tk := newLedgerTask()
	Flush(tk, nil)                 // no projection: no-op
	Flush(&Task{ID: "x"}, tk.Plan) // nil ledger plan: no-op
	Flush(nil, tk.Plan)            // nil task: no-op
	// Reaching here without a panic is the assertion.
}
