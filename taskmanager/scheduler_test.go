package taskmanager

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryScheduleStoreAddDueRemove(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryScheduleStore()
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	if err := store.Add(ctx, Schedule{ID: "b", Goal: "gb", FireAt: base.Add(2 * time.Minute)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(ctx, Schedule{ID: "a", Goal: "ga", FireAt: base.Add(1 * time.Minute)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Add(ctx, Schedule{ID: "c", Goal: "gc", FireAt: base.Add(3 * time.Minute)}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if due, err := store.Due(ctx, base); err != nil || len(due) != 0 {
		t.Fatalf("Due(base) = (%v, %v), want empty", due, err)
	}

	due, err := store.Due(ctx, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 2 || due[0].ID != "a" || due[1].ID != "b" {
		t.Fatalf("Due = %+v, want [a, b] in FireAt order", due)
	}

	if err := store.Remove(ctx, "a"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	due, err = store.Due(ctx, base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].ID != "b" {
		t.Fatalf("Due after remove = %+v, want [b]", due)
	}

	if err := store.Remove(ctx, "missing"); err != nil {
		t.Errorf("Remove(missing) = %v, want nil", err)
	}
}
