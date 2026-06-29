package taskmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/task"
)

func TestInMemoryMetaRoundTrip(t *testing.T) {
	store := NewInMemoryMetaStore()
	ctx := context.Background()
	in := &task.SessionMeta{
		TaskRefs:     []task.TaskRef{{ID: "t1", Title: "first"}},
		ActiveTaskID: "t1",
		Queue:        []string{"t2", "t3"},
	}
	if err := store.SaveMeta(ctx, "s1", in); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	out, err := store.LoadMeta(ctx, "s1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if out.ActiveTaskID != "t1" || len(out.Queue) != 2 || out.Queue[0] != "t2" || out.Queue[1] != "t3" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
	if len(out.TaskRefs) != 1 || out.TaskRefs[0].ID != "t1" {
		t.Errorf("TaskRefs mismatch: %+v", out.TaskRefs)
	}
}

func TestInMemoryMetaNotFound(t *testing.T) {
	store := NewInMemoryMetaStore()
	_, err := store.LoadMeta(context.Background(), "missing")
	if !errors.Is(err, ErrMetaNotFound) {
		t.Errorf("err = %v, want ErrMetaNotFound", err)
	}
}

func TestInMemoryMetaLoadIsDeepCopy(t *testing.T) {
	store := NewInMemoryMetaStore()
	ctx := context.Background()
	if err := store.SaveMeta(ctx, "s1", &task.SessionMeta{Queue: []string{"t1"}}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	got, _ := store.LoadMeta(ctx, "s1")
	got.Queue[0] = "mutated" // mutate the returned value
	again, _ := store.LoadMeta(ctx, "s1")
	if again.Queue[0] != "t1" {
		t.Errorf("store corrupted by mutating a returned value: %q", again.Queue[0])
	}
}

func TestInMemoryMetaSaveIsDeepCopy(t *testing.T) {
	store := NewInMemoryMetaStore()
	ctx := context.Background()
	in := &task.SessionMeta{Queue: []string{"t1"}}
	if err := store.SaveMeta(ctx, "s1", in); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	in.Queue[0] = "mutated" // mutate the caller's value after saving
	got, _ := store.LoadMeta(ctx, "s1")
	if got.Queue[0] != "t1" {
		t.Errorf("store corrupted by mutating the saved value: %q", got.Queue[0])
	}
}

func TestInMemoryMetaIsMetaStore(t *testing.T) {
	var _ MetaStore = NewInMemoryMetaStore()
}
