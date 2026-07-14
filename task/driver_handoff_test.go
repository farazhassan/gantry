package task

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

// handedOff scripts a run that terminates with DoneHandoff, exactly as a
// routing middleware would (state.Handoff set alongside the reason).
func handedOff(target string) func(*gantry.State) *gantry.State {
	return func(in *gantry.State) *gantry.State {
		in.Done = true
		in.DoneReason = gantry.DoneHandoff
		in.Handoff = &gantry.Handoff{Target: target, Mode: gantry.HandoffTransfer, Reason: "test route"}
		in.Usage = gantry.Usage{InputTokens: 1, OutputTokens: 1}
		return in
	}
}

func TestAdvanceDoneHandoffFailsWithClearCause(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		handedOff("billing"),
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-h1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want %q", got.Status, TaskFailed)
	}
	if runner.calls != 1 {
		t.Errorf("runner called %d times, want 1 (a handoff must terminate the drive loop, not continue it)", runner.calls)
	}
	if len(got.Working) == 0 {
		t.Fatal("Working is empty, want a recorded failure cause")
	}
	last := got.Working[len(got.Working)-1]
	if last.Role != gantry.RoleSystem {
		t.Errorf("last working message role = %q, want %q (the failure cause)", last.Role, gantry.RoleSystem)
	}
	if !strings.Contains(last.Content, "handoff is not supported inside task-driven runs yet") {
		t.Errorf("cause = %q, want it to name the unsupported handoff", last.Content)
	}
	if !strings.Contains(last.Content, "billing") {
		t.Errorf("cause = %q, want it to include the requested target", last.Content)
	}
}

func TestAdvanceDoneHandoffWithNilHandoffStillFailsCleanly(t *testing.T) {
	// Defensive: a broken middleware could set the reason without the record.
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		func(in *gantry.State) *gantry.State {
			in.Done = true
			in.DoneReason = gantry.DoneHandoff
			return in
		},
	}}
	d := NewDriver(runner, NewInMemory())
	tk := &Task{ID: "tk-h2", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want %q", got.Status, TaskFailed)
	}
	last := got.Working[len(got.Working)-1]
	if !strings.Contains(last.Content, "handoff is not supported inside task-driven runs yet") {
		t.Errorf("cause = %q, want the named handoff cause even with a nil state.Handoff", last.Content)
	}
}
