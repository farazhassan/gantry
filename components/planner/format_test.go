package planner

import (
	"testing"

	"github.com/farazhassan/gantry"
)

func TestRenderPlanWithoutStatusIsBackCompat(t *testing.T) {
	p := &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "first"},
		{Description: "second"},
	}}
	got := renderPlan(p)
	want := "\n\nPlan:\n1. first\n2. second\n"
	if got != want {
		t.Errorf("renderPlan() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderPlanWithStatusTags(t *testing.T) {
	p := &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "design", Status: gantry.StepDone},
		{Description: "build", Status: gantry.StepActive},
		{Description: "ship", Status: gantry.StepPending},
	}}
	got := renderPlan(p)
	want := "\n\nPlan:\n1. [done] design\n2. [active] build\n3. [pending] ship\n"
	if got != want {
		t.Errorf("renderPlan() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderPlanMixedStatus(t *testing.T) {
	// Tagged and status-less steps interleave: each renders in its own form.
	p := &gantry.Plan{Steps: []gantry.PlanStep{
		{Description: "design", Status: gantry.StepDone},
		{Description: "build"},
		{Description: "ship", Status: gantry.StepPending},
	}}
	got := renderPlan(p)
	want := "\n\nPlan:\n1. [done] design\n2. build\n3. [pending] ship\n"
	if got != want {
		t.Errorf("renderPlan() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderPlanEmpty(t *testing.T) {
	if got := renderPlan(&gantry.Plan{}); got != "" {
		t.Errorf("renderPlan(empty) = %q, want empty", got)
	}
	if got := renderPlan(nil); got != "" {
		t.Errorf("renderPlan(nil) = %q, want empty", got)
	}
}

func TestRenderPlanWithIDsAndCriteria(t *testing.T) {
	// The full ledger form: "N. [status] (id) description — criteria: ...".
	p := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Description: "design", Status: gantry.StepDone, AcceptanceCriteria: "spec approved"},
		{ID: "s2", Description: "build", Status: gantry.StepActive, AcceptanceCriteria: "tests pass"},
	}}
	got := renderPlan(p)
	want := "\n\nPlan:\n" +
		"1. [done] (s1) design — criteria: spec approved\n" +
		"2. [active] (s2) build — criteria: tests pass\n"
	if got != want {
		t.Errorf("renderPlan() =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderPlanPartialLedgerFields(t *testing.T) {
	// Each ledger field renders independently: id without status, criteria
	// without id, bare description. Pre-ledger planners keep their old output.
	p := &gantry.Plan{Steps: []gantry.PlanStep{
		{ID: "s1", Description: "design"},
		{Description: "build", AcceptanceCriteria: "tests pass"},
		{Description: "ship"},
	}}
	got := renderPlan(p)
	want := "\n\nPlan:\n" +
		"1. (s1) design\n" +
		"2. build — criteria: tests pass\n" +
		"3. ship\n"
	if got != want {
		t.Errorf("renderPlan() =\n%q\nwant\n%q", got, want)
	}
}
