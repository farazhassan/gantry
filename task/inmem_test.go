package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
)

func TestInMemorySaveLoadRoundTrip(t *testing.T) {
	s := NewInMemory()
	ctx := context.Background()
	in := &Task{
		ID:        "tk-1",
		SessionID: "sess-1",
		Title:     "T",
		Status:    TaskActive,
		Plan:      &gantry.Plan{Steps: []gantry.PlanStep{{ID: "s1", Description: "d", Status: gantry.StepActive}}},
		CreatedAt: time.Unix(100, 0),
	}
	if err := s.SaveTask(ctx, in); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	got, err := s.LoadTask(ctx, "tk-1")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if got.Title != "T" || got.Status != TaskActive || len(got.Plan.Steps) != 1 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestInMemoryLoadUnknown(t *testing.T) {
	s := NewInMemory()
	_, err := s.LoadTask(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInMemorySaveNilRejected(t *testing.T) {
	s := NewInMemory()
	if err := s.SaveTask(context.Background(), nil); err == nil {
		t.Error("SaveTask(nil) should error")
	}
}

func TestInMemoryReturnsIndependentCopies(t *testing.T) {
	// Mutating a loaded task must not corrupt the stored copy (store owns its
	// state), mirroring InMemoryCheckpointer's copy-on-load behavior.
	s := NewInMemory()
	ctx := context.Background()
	_ = s.SaveTask(ctx, &Task{ID: "tk-1", Title: "orig", Status: TaskPending})
	got, _ := s.LoadTask(ctx, "tk-1")
	got.Title = "mutated"
	again, _ := s.LoadTask(ctx, "tk-1")
	if again.Title != "orig" {
		t.Errorf("stored copy was mutated through a loaded task: %q", again.Title)
	}
}

func TestInMemorySaveIsolatesFromCaller(t *testing.T) {
	// Mutating the original task after saving must not corrupt the stored copy.
	s := NewInMemory()
	ctx := context.Background()
	in := &Task{ID: "tk-1", Title: "orig", Status: TaskPending}
	_ = s.SaveTask(ctx, in)
	in.Title = "mutated-after-save"
	got, _ := s.LoadTask(ctx, "tk-1")
	if got.Title != "orig" {
		t.Errorf("stored copy was mutated through the caller's reference after save: %q", got.Title)
	}
}

func TestInMemoryIsolatesStepMeta(t *testing.T) {
	// PlanStep.Meta is a map; the store must not share it with callers, or a
	// caller mutating a loaded step's Meta would corrupt the stored copy.
	s := NewInMemory()
	ctx := context.Background()
	in := &Task{
		ID:     "tk-1",
		Status: TaskPending,
		Plan:   &gantry.Plan{Steps: []gantry.PlanStep{{ID: "s1", Meta: map[string]any{"k": "v1"}}}},
	}
	_ = s.SaveTask(ctx, in)

	// Mutate via the caller's reference after save and via a loaded copy.
	in.Plan.Steps[0].Meta["k"] = "mutated-after-save"
	got, _ := s.LoadTask(ctx, "tk-1")
	got.Plan.Steps[0].Meta["k"] = "mutated-via-load"

	again, _ := s.LoadTask(ctx, "tk-1")
	if again.Plan.Steps[0].Meta["k"] != "v1" {
		t.Errorf("stored step Meta was mutated through a shared map: %v", again.Plan.Steps[0].Meta["k"])
	}
}

func TestInMemoryListBySession(t *testing.T) {
	s := NewInMemory()
	ctx := context.Background()
	_ = s.SaveTask(ctx, &Task{ID: "a", SessionID: "sess-1", Title: "A", Status: TaskPending, CreatedAt: time.Unix(1, 0)})
	_ = s.SaveTask(ctx, &Task{ID: "b", SessionID: "sess-1", Title: "B", Status: TaskActive, CreatedAt: time.Unix(2, 0)})
	_ = s.SaveTask(ctx, &Task{ID: "c", SessionID: "sess-2", Title: "C", Status: TaskPending, CreatedAt: time.Unix(3, 0)})

	refs, err := s.ListBySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len(refs) = %d, want 2", len(refs))
	}
	// Ordered by CreatedAt ascending (append-only history).
	if refs[0].ID != "a" || refs[1].ID != "b" {
		t.Errorf("order = [%s, %s], want [a, b]", refs[0].ID, refs[1].ID)
	}
	// A re-save updates the existing ref's status rather than duplicating it.
	_ = s.SaveTask(ctx, &Task{ID: "a", SessionID: "sess-1", Title: "A", Status: TaskDone, CreatedAt: time.Unix(1, 0)})
	refs, _ = s.ListBySession(ctx, "sess-1")
	if len(refs) != 2 {
		t.Fatalf("re-save duplicated a ref: len = %d", len(refs))
	}
	if refs[0].Status != TaskDone {
		t.Errorf("re-save did not update status: %+v", refs[0])
	}
}

func TestInMemoryListUnknownSessionEmpty(t *testing.T) {
	s := NewInMemory()
	refs, err := s.ListBySession(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("want empty, got %d", len(refs))
	}
}
