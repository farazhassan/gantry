package taskmanager

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/farazhassan/gantry/task"
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

// fakeClock is a manually-advanced clock for deterministic scheduler tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// drainReady dequeues every session id currently on the ready queue.
func drainReady(t *testing.T, ctx context.Context, ready *InMemoryReadyQueue) []string {
	t.Helper()
	var out []string
	for {
		sid, ok, err := ready.Dequeue(ctx)
		if err != nil {
			t.Fatalf("Dequeue: %v", err)
		}
		if !ok {
			return out
		}
		out = append(out, sid)
	}
}

func TestSchedulerFiresDueSchedule(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(base)

	s := NewScheduler(tm, NewInMemoryScheduleStore(),
		WithSchedulePollInterval(time.Millisecond),
		WithScheduleClock(clk.Now),
	)
	if _, err := s.Schedule(ctx, "the goal", "the title", base.Add(time.Minute)); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	s.Start(ctx)
	defer s.Stop()

	// Not yet due: clock still at base. Give the loop a few ticks; nothing fires.
	time.Sleep(20 * time.Millisecond)
	if got := drainReady(t, ctx, ready); len(got) != 0 {
		t.Fatalf("fired early: ready had %v", got)
	}

	// Advance past FireAt; the schedule fires on the next tick.
	clk.Advance(2 * time.Minute)

	var sid string
	waitFor(t, func() bool {
		got := drainReady(t, ctx, ready)
		if len(got) == 1 {
			sid = got[0]
			return true
		}
		return false
	})

	sm, err := meta.LoadMeta(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMeta(%q): %v", sid, err)
	}
	tk, err := tasks.LoadTask(ctx, sm.ActiveTaskID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Goal != "the goal" || tk.Title != "the title" {
		t.Errorf("fired task = {goal:%q title:%q}, want {the goal, the title}", tk.Goal, tk.Title)
	}
}

func TestSchedulerFiresAtMostOnce(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	var sessions int32
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithSessionIDFunc(func() string {
			return fmt.Sprintf("s%d", atomic.AddInt32(&sessions, 1))
		}),
	)
	ctx := context.Background()
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(base.Add(time.Hour)) // already past FireAt

	s := NewScheduler(tm, NewInMemoryScheduleStore(),
		WithSchedulePollInterval(time.Millisecond),
		WithScheduleClock(clk.Now),
	)
	if _, err := s.Schedule(ctx, "g", "", base); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	s.Start(ctx)
	defer s.Stop()

	waitFor(t, func() bool { return atomic.LoadInt32(&sessions) >= 1 })
	time.Sleep(30 * time.Millisecond)
	if n := atomic.LoadInt32(&sessions); n != 1 {
		t.Errorf("fired %d times, want exactly 1 (at-most-once)", n)
	}
}

func TestSchedulerFiresFIFOByFireAt(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	tm := NewTaskManager(driver, tasks, meta, ready)
	ctx := context.Background()
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(base.Add(time.Hour)) // all three already due

	s := NewScheduler(tm, NewInMemoryScheduleStore(),
		WithSchedulePollInterval(time.Millisecond),
		WithScheduleClock(clk.Now),
	)
	if _, err := s.Schedule(ctx, "second", "", base.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(ctx, "first", "", base.Add(1*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(ctx, "third", "", base.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	defer s.Stop()

	var sids []string
	waitFor(t, func() bool {
		sids = append(sids, drainReady(t, ctx, ready)...)
		return len(sids) == 3
	})

	var goals []string
	for _, sid := range sids {
		sm, _ := meta.LoadMeta(ctx, sid)
		tk, _ := tasks.LoadTask(ctx, sm.ActiveTaskID)
		goals = append(goals, tk.Goal)
	}
	want := []string{"first", "second", "third"}
	for i := range want {
		if goals[i] != want[i] {
			t.Fatalf("firing order = %v, want %v", goals, want)
		}
	}
}

// flakyDueStore wraps InMemoryScheduleStore and forces the first N Due calls to
// error, to exercise the scheduler's log-and-continue path.
type flakyDueStore struct {
	*InMemoryScheduleStore
	mu      sync.Mutex
	dueErrs int
}

func (f *flakyDueStore) Due(ctx context.Context, now time.Time) ([]Schedule, error) {
	f.mu.Lock()
	if f.dueErrs > 0 {
		f.dueErrs--
		f.mu.Unlock()
		return nil, errSentinel{}
	}
	f.mu.Unlock()
	return f.InMemoryScheduleStore.Due(ctx, now)
}

func TestSchedulerErrorHandlerFiresAndLoopContinues(t *testing.T) {
	tm, _, _, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	base := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	clk := newFakeClock(base.Add(time.Hour))

	store := &flakyDueStore{InMemoryScheduleStore: NewInMemoryScheduleStore(), dueErrs: 1}
	errs := make(chan error, 8)
	s := NewScheduler(tm, store,
		WithSchedulePollInterval(time.Millisecond),
		WithScheduleClock(clk.Now),
		WithScheduleErrorHandler(func(err error) { errs <- err }),
	)
	if _, err := s.Schedule(ctx, "g", "", base); err != nil {
		t.Fatal(err)
	}
	s.Start(ctx)
	defer s.Stop()

	select {
	case err := <-errs:
		if err == nil {
			t.Errorf("error handler received nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error handler never fired")
	}
	waitFor(t, func() bool { return len(drainReady(t, ctx, ready)) == 1 })
}

func TestSchedulerIdlesOnEmptyStoreAndStopsPromptly(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	var errCount int
	var mu sync.Mutex

	s := NewScheduler(tm, NewInMemoryScheduleStore(),
		WithSchedulePollInterval(time.Millisecond),
		WithScheduleErrorHandler(func(error) { mu.Lock(); errCount++; mu.Unlock() }),
	)
	s.Start(ctx)
	time.Sleep(20 * time.Millisecond)

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return promptly")
	}
	mu.Lock()
	defer mu.Unlock()
	if errCount != 0 {
		t.Errorf("errHandler fired %d times on empty store, want 0", errCount)
	}
}

func TestSchedulerCtxCancelStopsLoop(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	ctx, cancel := context.WithCancel(context.Background())
	s := NewScheduler(tm, NewInMemoryScheduleStore(), WithSchedulePollInterval(time.Millisecond))
	s.Start(ctx)
	cancel()

	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after ctx cancellation")
	}
}
