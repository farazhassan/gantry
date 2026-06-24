package planner

import (
	"strings"
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
	for _, want := range []string{"1. [done] design", "2. [active] build", "3. [pending] ship"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderPlan() missing %q in:\n%s", want, got)
		}
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
