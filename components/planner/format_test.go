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
