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

func TestHydrateIsolatesStepMeta(t *testing.T) {
	// Meta is a map; mutating it on the projection must not reach the ledger.
	tk := &Task{
		ID:   "tk-1",
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{{ID: "s1", Meta: map[string]any{"k": "v1"}}}},
	}
	proj := Hydrate(tk)
	proj.Steps[0].Meta["k"] = "mutated"
	if tk.Plan.Steps[0].Meta["k"] != "v1" {
		t.Errorf("ledger step Meta mutated through projection: %v", tk.Plan.Steps[0].Meta["k"])
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

func TestFlushAdoptsProjectionOnlySteps(t *testing.T) {
	tk := newLedgerTask()
	// The run updated s2 and appended a step the ledger has never seen (the
	// update_plan tool does exactly this mid-run). The unknown step must be
	// ADOPTED with its id — not silently dropped.
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s2", Status: gantry.StepDone},
		{ID: "s4", Description: "write docs", Status: gantry.StepPending, AcceptanceCriteria: "README updated"},
	}}
	Flush(tk, proj)
	if tk.Plan.Steps[1].Status != gantry.StepDone {
		t.Errorf("s2 not updated: %+v", tk.Plan.Steps[1])
	}
	if len(tk.Plan.Steps) != 4 {
		t.Fatalf("step count = %d, want 4 (s4 adopted)", len(tk.Plan.Steps))
	}
	got := tk.Plan.Steps[3]
	if got.ID != "s4" || got.Description != "write docs" || got.AcceptanceCriteria != "README updated" {
		t.Errorf("adopted step = %+v, want s4 carried over wholesale", got)
	}
	// Ledger steps absent from the projection stay untouched.
	if tk.Plan.Steps[0].Status != gantry.StepDone || tk.Plan.Steps[2].Status != gantry.StepPending {
		t.Errorf("unmatched steps were mutated: %+v / %+v", tk.Plan.Steps[0], tk.Plan.Steps[2])
	}
}

func TestFlushReMintsEmptyAndCollidingAdoptedIDs(t *testing.T) {
	tk := newLedgerTask() // ledger owns s1..s3
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "no id"},               // empty id: minted
		{ID: "n1", Description: "first n1"},  // fresh id: preserved
		{ID: "n1", Description: "second n1"}, // in-projection duplicate: re-minted
	}}
	Flush(tk, proj)
	if len(tk.Plan.Steps) != 6 {
		t.Fatalf("step count = %d, want 6", len(tk.Plan.Steps))
	}
	a, b, c := tk.Plan.Steps[3], tk.Plan.Steps[4], tk.Plan.Steps[5]
	if a.ID != "s4" {
		t.Errorf("empty-id step got %q, want minted s4", a.ID)
	}
	if b.ID != "n1" {
		t.Errorf("fresh id clobbered: %q", b.ID)
	}
	if c.ID == "n1" || c.ID == "" {
		t.Errorf("duplicate id survived adoption: %q", c.ID)
	}
	seen := map[string]bool{}
	for _, s := range tk.Plan.Steps {
		if seen[s.ID] {
			t.Errorf("duplicate ledger id %q after flush", s.ID)
		}
		seen[s.ID] = true
	}
}

func TestFlushAdoptedStepMetaIsolated(t *testing.T) {
	tk := newLedgerTask()
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "n1", Description: "new", Meta: map[string]any{"k": "v"}},
	}}
	Flush(tk, proj)
	proj.Steps[0].Meta["k"] = "mutated"
	if tk.Plan.Steps[3].Meta["k"] != "v" {
		t.Errorf("adopted step shares its Meta map with the projection: %v", tk.Plan.Steps[3].Meta["k"])
	}
}

func TestFlushNilSafe(t *testing.T) {
	tk := newLedgerTask()
	Flush(tk, nil)                 // no projection: no-op
	Flush(&Task{ID: "x"}, tk.Plan) // nil ledger plan: no-op
	Flush(nil, tk.Plan)            // nil task: no-op
	// Reaching here without a panic is the assertion.
}
