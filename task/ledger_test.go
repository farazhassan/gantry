package task

import (
	"fmt"
	"strings"
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

func TestHydrateBoundsDoneStepOutput(t *testing.T) {
	// Multibyte runes prove the budget counts runes, not bytes.
	long := strings.Repeat("界", DefaultOutputRuneBudget+42)
	tk := &Task{ID: "tk-1", Plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: long},
		{ID: "s2", Status: gantry.StepActive, Output: long}, // non-done: projected in full
	}}}

	proj := Hydrate(tk)
	want := strings.Repeat("界", DefaultOutputRuneBudget) +
		fmt.Sprintf("… (truncated, %d total)", DefaultOutputRuneBudget+42)
	if proj.Steps[0].Output != want {
		t.Errorf("done-step output = %q, want %q", proj.Steps[0].Output, want)
	}
	if proj.Steps[1].Output != long {
		t.Errorf("active-step output was truncated; must be projected in full")
	}
	if tk.Plan.Steps[0].Output != long {
		t.Errorf("ledger output changed by Hydrate; the ledger keeps the full record")
	}
}

func TestHydrateShortOutputUnchanged(t *testing.T) {
	tk := &Task{ID: "tk-1", Plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: "small"},
	}}}
	if got := Hydrate(tk).Steps[0].Output; got != "small" {
		t.Errorf("under-budget output = %q, want unchanged %q", got, "small")
	}
}

func TestHydrateBoundedZeroIsUnlimited(t *testing.T) {
	long := strings.Repeat("a", DefaultOutputRuneBudget*3)
	tk := &Task{ID: "tk-1", Plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: long},
	}}}
	if got := HydrateBounded(tk, 0).Steps[0].Output; got != long {
		t.Errorf("HydrateBounded(t, 0) truncated; a non-positive budget means unbounded")
	}
}

func TestFlushDoesNotClobberSettledDoneOutput(t *testing.T) {
	// A step that was done and stayed done keeps its ledger Output: the
	// projection's copy may be the truncated form from HydrateBounded.
	tk := &Task{ID: "tk-1", Plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: "the full record"},
		{ID: "s2", Status: gantry.StepActive},
	}}}
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: "the full re… (truncated, 15 total)"},
		{ID: "s2", Status: gantry.StepDone, Output: "fresh result"},
	}}

	Flush(tk, proj)

	if tk.Plan.Steps[0].Output != "the full record" {
		t.Errorf("settled done Output clobbered: %q", tk.Plan.Steps[0].Output)
	}
	if tk.Plan.Steps[1].Status != gantry.StepDone || tk.Plan.Steps[1].Output != "fresh result" {
		t.Errorf("newly-done step not reconciled: %+v", tk.Plan.Steps[1])
	}
}

func TestFlushCopiesOutputWhenDoneStepReopened(t *testing.T) {
	tk := &Task{ID: "tk-1", Plan: &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepDone, Output: "old"},
	}}}
	proj := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Status: gantry.StepActive, Output: "reworking"},
	}}
	Flush(tk, proj)
	if tk.Plan.Steps[0].Status != gantry.StepActive || tk.Plan.Steps[0].Output != "reworking" {
		t.Errorf("re-opened step not reconciled: %+v", tk.Plan.Steps[0])
	}
}
