package task

import (
	"errors"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
)

func TestTaskStatusIsTerminal(t *testing.T) {
	terminal := []TaskStatus{TaskDone, TaskFailed, TaskCancelled}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Errorf("%q should be terminal", s)
		}
	}
	nonTerminal := []TaskStatus{TaskPending, TaskActive, TaskAwaitingInput}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Errorf("%q should not be terminal", s)
		}
	}
}

func TestTaskZeroValueAndConstruction(t *testing.T) {
	now := time.Now()
	tk := &Task{
		ID:        "tk-1",
		SessionID: "sess-1",
		Title:     "Ship the thing",
		Goal:      "Ship the thing end to end",
		Status:    TaskPending,
		Plan:      &gantry.Plan{Goal: "g", Steps: []gantry.PlanStep{{ID: "s1", Description: "step"}}},
		Budget:    TaskBudget{MaxRuns: 5},
		Working:   []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if tk.Status != TaskPending {
		t.Fatalf("Status = %q", tk.Status)
	}
	if tk.Budget.MaxRuns != 5 || tk.Budget.UsedRuns != 0 {
		t.Errorf("budget = %+v", tk.Budget)
	}
}

func TestErrNotFoundIsSentinel(t *testing.T) {
	wrapped := errWrap(ErrNotFound, "tk-x")
	if !errors.Is(wrapped, ErrNotFound) {
		t.Errorf("wrapped error should match ErrNotFound via errors.Is")
	}
}
