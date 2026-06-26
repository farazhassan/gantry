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

func TestNewSchedulerDefaults(t *testing.T) {
	s := NewScheduler(&TaskManager{}, NewInMemoryScheduleStore())
	if s.interval != time.Second {
		t.Errorf("default interval = %v, want 1s", s.interval)
	}
	if s.clock == nil {
		t.Errorf("default clock is nil, want time.Now")
	}
	if s.errHandler == nil {
		t.Errorf("default errHandler is nil, want non-nil no-op")
	}
	if s.newID == nil {
		t.Errorf("default newID is nil")
	}
	s.errHandler(nil) // no-op must be safe to call
}

func TestNewSchedulerOptions(t *testing.T) {
	fixed := time.Date(2026, 6, 26, 0, 0, 0, 0, time.UTC)
	var got error
	s := NewScheduler(&TaskManager{}, NewInMemoryScheduleStore(),
		WithSchedulePollInterval(5*time.Millisecond),
		WithScheduleClock(func() time.Time { return fixed }),
		WithScheduleErrorHandler(func(err error) { got = err }),
		WithScheduleIDFunc(func() string { return "fixed-id" }),
	)
	if s.interval != 5*time.Millisecond {
		t.Errorf("interval = %v, want 5ms", s.interval)
	}
	if !s.clock().Equal(fixed) {
		t.Errorf("clock() = %v, want %v", s.clock(), fixed)
	}
	if s.newID() != "fixed-id" {
		t.Errorf("newID() = %q, want fixed-id", s.newID())
	}
	sentinel := errSentinel{}
	s.errHandler(sentinel)
	if got != sentinel {
		t.Errorf("errHandler did not capture the error")
	}
}

func TestNewSchedulerNilArgsPanic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tm    *TaskManager
		store ScheduleStore
	}{
		{"nil tm", nil, NewInMemoryScheduleStore()},
		{"nil store", &TaskManager{}, nil},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Errorf("NewScheduler(%s) did not panic", tc.name)
				}
			}()
			NewScheduler(tc.tm, tc.store)
		})
	}
}

func TestNewSchedulerNonPositiveIntervalPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("WithSchedulePollInterval(0) did not panic")
		}
	}()
	NewScheduler(&TaskManager{}, NewInMemoryScheduleStore(), WithSchedulePollInterval(0))
}

func TestSchedulerScheduleAddsToStore(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryScheduleStore()
	s := NewScheduler(&TaskManager{}, store, WithScheduleIDFunc(func() string { return "sch-1" }))
	at := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	id, err := s.Schedule(ctx, "do x", "X", at)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if id != "sch-1" {
		t.Errorf("id = %q, want sch-1", id)
	}
	due, err := store.Due(ctx, at)
	if err != nil {
		t.Fatalf("Due: %v", err)
	}
	if len(due) != 1 || due[0].ID != "sch-1" || due[0].Goal != "do x" || due[0].Title != "X" || !due[0].FireAt.Equal(at) {
		t.Errorf("stored schedule = %+v, want {sch-1, do x, X, %v}", due, at)
	}
}

func TestSchedulerDoubleStartPanics(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	s := NewScheduler(tm, NewInMemoryScheduleStore(), WithSchedulePollInterval(time.Millisecond))
	s.Start(context.Background())
	defer s.Stop()
	defer func() {
		if recover() == nil {
			t.Errorf("second Start did not panic")
		}
	}()
	s.Start(context.Background())
}

func TestSchedulerStopIsIdempotent(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	s := NewScheduler(tm, NewInMemoryScheduleStore(), WithSchedulePollInterval(time.Millisecond))
	s.Start(context.Background())
	s.Stop()
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop blocked")
	}
}

func TestSchedulerStopBeforeStartIsNoOp(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	s := NewScheduler(tm, NewInMemoryScheduleStore(), WithSchedulePollInterval(time.Millisecond))
	done := make(chan struct{})
	go func() { s.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop before Start blocked")
	}
}
