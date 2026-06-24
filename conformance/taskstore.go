package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

// TaskStoreSuite verifies the contract of task.TaskStore.
func TaskStoreSuite(t *testing.T, factory func() task.TaskStore) {
	t.Helper()

	t.Run("save_then_load_round_trip", func(t *testing.T) {
		s := factory()
		ctx := context.Background()
		want := &task.Task{
			ID:        "tk-1",
			SessionID: "sess-1",
			Title:     "title",
			Goal:      "goal",
			Status:    task.TaskActive,
			Plan:      &gantry.Plan{Steps: []gantry.PlanStep{{ID: "s1", Description: "d", Status: gantry.StepActive}}},
		}
		if err := s.SaveTask(ctx, want); err != nil {
			t.Fatalf("SaveTask: %v", err)
		}
		got, err := s.LoadTask(ctx, "tk-1")
		if err != nil {
			t.Fatalf("LoadTask: %v", err)
		}
		if got.Title != want.Title || got.Goal != want.Goal ||
			got.SessionID != want.SessionID || got.Status != want.Status ||
			len(got.Plan.Steps) != 1 {
			t.Errorf("round-trip mismatch: %+v", got)
		}
		if s0 := got.Plan.Steps[0]; s0.ID != "s1" || s0.Description != "d" || s0.Status != gantry.StepActive {
			t.Errorf("round-trip lost step content: %+v", s0)
		}
	})

	t.Run("load_unknown_returns_ErrNotFound", func(t *testing.T) {
		s := factory()
		_, err := s.LoadTask(context.Background(), "ghost")
		if err == nil {
			t.Fatalf("expected error for unknown id")
		}
		if !errors.Is(err, task.ErrNotFound) {
			t.Errorf("expected error to wrap task.ErrNotFound, got %v", err)
		}
	})

	t.Run("overwrite_same_id", func(t *testing.T) {
		s := factory()
		ctx := context.Background()
		_ = s.SaveTask(ctx, &task.Task{ID: "tk-2", Title: "v1", Status: task.TaskPending})
		_ = s.SaveTask(ctx, &task.Task{ID: "tk-2", Title: "v2", Status: task.TaskActive})
		got, _ := s.LoadTask(ctx, "tk-2")
		if got.Title != "v2" || got.Status != task.TaskActive {
			t.Errorf("overwrite failed: %+v", got)
		}
	})

	t.Run("list_by_session_filters_and_unknown_is_empty", func(t *testing.T) {
		s := factory()
		ctx := context.Background()
		_ = s.SaveTask(ctx, &task.Task{ID: "a", SessionID: "sess-1", Status: task.TaskPending})
		_ = s.SaveTask(ctx, &task.Task{ID: "b", SessionID: "sess-2", Status: task.TaskPending})
		refs, err := s.ListBySession(ctx, "sess-1")
		if err != nil {
			t.Fatalf("ListBySession: %v", err)
		}
		if len(refs) != 1 || refs[0].ID != "a" {
			t.Errorf("filter failed: %+v", refs)
		}
		empty, err := s.ListBySession(ctx, "nobody")
		if err != nil {
			t.Fatalf("ListBySession(unknown): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("unknown session should be empty, got %d", len(empty))
		}
	})

	t.Run("save_nil_is_rejected", func(t *testing.T) {
		s := factory()
		if err := s.SaveTask(context.Background(), nil); err == nil {
			t.Error("SaveTask(nil) should error")
		}
	})
}
