package task

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestAdoptAssignsIDsToIDlessSteps(t *testing.T) {
	tk := &Task{ID: "tk-1"} // empty ledger
	projected := &gantry.Plan{
		Goal: "ship it",
		Steps: []gantry.PlanStep{
			{Description: "design", Status: gantry.StepActive},
			{Description: "build"},
		},
	}
	adoptOrFlush(tk, projected)
	if tk.Plan == nil || len(tk.Plan.Steps) != 2 {
		t.Fatalf("plan not adopted: %+v", tk.Plan)
	}
	if tk.Plan.Steps[0].ID != "s1" || tk.Plan.Steps[1].ID != "s2" {
		t.Errorf("IDs = %q,%q want s1,s2", tk.Plan.Steps[0].ID, tk.Plan.Steps[1].ID)
	}
	if tk.Plan.Goal != "ship it" || tk.Plan.Steps[0].Status != gantry.StepActive {
		t.Errorf("adopt did not copy plan content faithfully: %+v", tk.Plan)
	}
}

func TestAdoptPreservesExistingIDs(t *testing.T) {
	tk := &Task{ID: "tk-1"}
	projected := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "custom", Description: "a"},
		{Description: "b"},
	}}
	adoptOrFlush(tk, projected)
	if tk.Plan.Steps[0].ID != "custom" {
		t.Errorf("existing ID clobbered: %q", tk.Plan.Steps[0].ID)
	}
	if tk.Plan.Steps[1].ID != "s2" {
		t.Errorf("ID-less step got %q, want s2", tk.Plan.Steps[1].ID)
	}
}

func TestAdoptIsIndependentDeepCopy(t *testing.T) {
	tk := &Task{ID: "tk-1"}
	projected := &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "a", Meta: map[string]any{"k": "v"}},
	}}
	adoptOrFlush(tk, projected)
	projected.Steps[0].Description = "mutated"
	projected.Steps[0].Meta["k"] = "mutated"
	if tk.Plan.Steps[0].Description != "a" {
		t.Errorf("adopted step shares Description backing: %q", tk.Plan.Steps[0].Description)
	}
	if tk.Plan.Steps[0].Meta["k"] != "v" {
		t.Errorf("adopted step shares Meta map: %v", tk.Plan.Steps[0].Meta["k"])
	}
}

func TestAdoptOrFlushReconcilesWhenLedgerHasSteps(t *testing.T) {
	tk := &Task{
		ID: "tk-1",
		Plan: &gantry.Plan{Steps: []gantry.PlanStep{
			{ID: "s1", Description: "design", Status: gantry.StepActive},
			{ID: "s2", Description: "build", Status: gantry.StepPending},
		}},
	}
	projected := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Description: "design", Status: gantry.StepDone, Output: "spec.md"},
		{ID: "s2", Description: "build", Status: gantry.StepActive},
	}}
	adoptOrFlush(tk, projected)
	if tk.Plan.Steps[0].Status != gantry.StepDone || tk.Plan.Steps[0].Output != "spec.md" {
		t.Errorf("s1 not reconciled via Flush: %+v", tk.Plan.Steps[0])
	}
	if tk.Plan.Steps[1].Status != gantry.StepActive {
		t.Errorf("s2 not reconciled via Flush: %+v", tk.Plan.Steps[1])
	}
}

func TestAdoptOrFlushNilProjectionIsNoop(t *testing.T) {
	tk := &Task{ID: "tk-1"}
	adoptOrFlush(tk, nil)
	if tk.Plan != nil {
		t.Errorf("nil projection must not create a plan: %+v", tk.Plan)
	}
	adoptOrFlush(tk, &gantry.Plan{})
	if tk.Plan != nil {
		t.Errorf("zero-step projection must not create a plan: %+v", tk.Plan)
	}
}
