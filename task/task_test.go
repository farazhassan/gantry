package task

import (
	"context"
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

func TestTaskParentLinkageFieldsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := NewInMemory()
	in := &Task{
		ID:              "tk-child",
		SessionID:       "sess-child",
		Title:           "child",
		Goal:            "do child things",
		Status:          TaskPending,
		ParentSessionID: "sess-parent",
		ParentTaskID:    "tk-parent",
		Depth:           2,
		AgentProfile:    "researcher",
		Budget:          TaskBudget{MaxRuns: 3},
	}
	if err := s.SaveTask(ctx, in); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := s.LoadTask(ctx, "tk-child")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if got.ParentSessionID != "sess-parent" || got.ParentTaskID != "tk-parent" {
		t.Errorf("parent linkage = (%q,%q), want (sess-parent, tk-parent)", got.ParentSessionID, got.ParentTaskID)
	}
	if got.Depth != 2 {
		t.Errorf("Depth = %d, want 2", got.Depth)
	}
	if got.AgentProfile != "researcher" {
		t.Errorf("AgentProfile = %q, want researcher", got.AgentProfile)
	}
}

func TestSessionMetaChildRefs(t *testing.T) {
	sm := &SessionMeta{
		ChildRefs: []ChildRef{{SessionID: "sess-c", TaskID: "tk-c", Title: "child"}},
	}
	if len(sm.ChildRefs) != 1 {
		t.Fatalf("ChildRefs len = %d, want 1", len(sm.ChildRefs))
	}
	if sm.ChildRefs[0] != (ChildRef{SessionID: "sess-c", TaskID: "tk-c", Title: "child"}) {
		t.Errorf("ChildRefs[0] = %+v, want {sess-c tk-c child}", sm.ChildRefs[0])
	}
}
