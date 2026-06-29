package gantry

import "testing"

func TestStepStatusValues(t *testing.T) {
	cases := []struct {
		s    StepStatus
		want string
	}{
		{StepPending, "pending"},
		{StepActive, "active"},
		{StepDone, "done"},
		{StepFailed, "failed"},
		{StepSkipped, "skipped"},
	}
	for _, c := range cases {
		if string(c.s) != c.want {
			t.Errorf("StepStatus = %q, want %q", c.s, c.want)
		}
	}
}

func TestPlanStepZeroValueBackCompat(t *testing.T) {
	// A step built the old way (Description only) must keep working: new
	// fields are zero-valued and Status is the empty string, not a panic.
	step := PlanStep{Description: "do the thing"}
	if step.Status != "" {
		t.Errorf("zero-value Status = %q, want empty", step.Status)
	}
	if step.ID != "" || step.AcceptanceCriteria != "" || step.Output != "" {
		t.Errorf("zero-value extra fields not empty: %+v", step)
	}
}
