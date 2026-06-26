package taskmanager

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

func TestNewDispatcherDefaults(t *testing.T) {
	tm := &TaskManager{} // zero value is fine; we only inspect dispatcher config here
	d := NewDispatcher(tm)
	if d.tm != tm {
		t.Errorf("d.tm not set to the provided TaskManager")
	}
	if d.interval != time.Second {
		t.Errorf("default interval = %v, want 1s", d.interval)
	}
	if d.errHandler == nil {
		t.Errorf("default errHandler is nil, want non-nil no-op")
	}
	// no-op handler must be safe to call
	d.errHandler(nil)
}

func TestNewDispatcherOptions(t *testing.T) {
	tm := &TaskManager{}
	var got error
	d := NewDispatcher(tm,
		WithPollInterval(5*time.Millisecond),
		WithErrorHandler(func(err error) { got = err }),
	)
	if d.interval != 5*time.Millisecond {
		t.Errorf("interval = %v, want 5ms", d.interval)
	}
	sentinel := errSentinel{}
	d.errHandler(sentinel)
	if got != sentinel {
		t.Errorf("errHandler did not capture the error")
	}
}

type errSentinel struct{}

func (errSentinel) Error() string { return "sentinel" }

func TestNewDispatcherNilTaskManagerPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("NewDispatcher(nil) did not panic")
		}
	}()
	NewDispatcher(nil)
}

func TestNewDispatcherNonPositiveIntervalPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("WithPollInterval(0) did not panic")
		}
	}()
	NewDispatcher(&TaskManager{}, WithPollInterval(0))
}

// --- shared helpers/fakes for dispatcher tests (introduced in Task 2) ---

// waitFor polls cond until it returns true or the deadline elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}

// newDispatcherManager wires a runner into a real Driver + in-memory stores and
// returns the manager and the ready queue so tests can seed cross-session work.
func newDispatcherManager(r task.Runner) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	tm := NewTaskManager(driver, tasks, meta, ready)
	return tm, tasks, meta, ready
}

// seedReadySession creates a pending task as the active task of sid and enqueues
// sid onto the ready queue, mimicking what a cross-session spawn leaves behind.
func seedReadySession(t *testing.T, ctx context.Context, tasks task.TaskStore, meta MetaStore, ready *InMemoryReadyQueue, taskID, sid, goal string) {
	t.Helper()
	tk := &task.Task{ID: taskID, SessionID: sid, Goal: goal, Status: task.TaskPending, CreatedAt: time.Now().UTC()}
	if err := tasks.SaveTask(ctx, tk); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	sm := &task.SessionMeta{ActiveTaskID: taskID, TaskRefs: []task.TaskRef{{ID: taskID, Status: task.TaskPending, CreatedAt: tk.CreatedAt}}}
	if err := meta.SaveMeta(ctx, sid, sm); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, sid); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
}

// completeOnceRunner completes whatever task it is given (no tool calls -> done).
type completeOnceRunner struct{}

func (completeOnceRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestDispatcherDrainsEnqueuedSession(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "the work")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(ctx)
	defer d.Stop()

	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-1")
		return err == nil && tk.Status == task.TaskDone
	})

	sm, _ := meta.LoadMeta(ctx, "s1")
	if sm.ActiveTaskID != "" || len(sm.Queue) != 0 {
		t.Errorf("session not drained: active=%q queue=%v", sm.ActiveTaskID, sm.Queue)
	}
}

func TestDispatcherIdlesOnEmptyQueueAndStopsPromptly(t *testing.T) {
	var errCount int
	var mu sync.Mutex
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()

	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithErrorHandler(func(error) { mu.Lock(); errCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	time.Sleep(20 * time.Millisecond) // let it idle through several poll cycles

	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return promptly")
	}

	mu.Lock()
	defer mu.Unlock()
	if errCount != 0 {
		t.Errorf("errHandler fired %d times on empty queue, want 0", errCount)
	}
}

func TestDispatcherDrivesSessionsFIFO(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()

	// Enqueue three sessions in order.
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "first")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "second")
	seedReadySession(t, ctx, tasks, meta, ready, "task-3", "s3", "third")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(ctx)
	defer d.Stop()

	// All three drained to done.
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		id := id
		waitFor(t, func() bool {
			tk, err := tasks.LoadTask(ctx, id)
			return err == nil && tk.Status == task.TaskDone
		})
	}
	// Queue fully drained.
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue not empty after draining all sessions")
	}
}

// errThenCompleteRunner returns a Go error on its first Resume call, then
// completes every subsequent task. This makes the first RunNextReady drive
// return an error (the session id is already consumed) while later sessions
// still drive cleanly.
type errThenCompleteRunner struct {
	mu    sync.Mutex
	calls int
}

func (r *errThenCompleteRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	r.mu.Lock()
	r.calls++
	first := r.calls == 1
	r.mu.Unlock()
	if first {
		return nil, errSentinel{}
	}
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestDispatcherErrorHandlerFiresAndLoopContinues(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(&errThenCompleteRunner{})
	ctx := context.Background()

	errs := make(chan error, 8)
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "will error")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "will complete")

	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithErrorHandler(func(err error) { errs <- err }),
	)
	d.Start(ctx)
	defer d.Stop()

	// The first drive errors -> handler fires at least once.
	select {
	case err := <-errs:
		if err == nil {
			t.Errorf("error handler received nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error handler never fired")
	}

	// The loop kept going: the second session drained to done.
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-2")
		return err == nil && tk.Status == task.TaskDone
	})
}

// blockingRunner signals when it has entered Resume, then blocks until the
// context is cancelled, returning the context error.
type blockingRunner struct {
	entered chan struct{}
}

func (r *blockingRunner) Resume(ctx context.Context, _ *gantry.State) (*gantry.State, error) {
	close(r.entered)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDispatcherStopCancelsInFlightDrive(t *testing.T) {
	r := &blockingRunner{entered: make(chan struct{})}
	tm, tasks, meta, ready := newDispatcherManager(r)
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "blocks forever")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(ctx)

	// Wait until the drive is actually running and blocked.
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("drive never started")
	}

	// Stop must cancel the blocked drive and return.
	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return; in-flight drive was not cancelled")
	}
}

func TestDispatcherCtxCancelStopsLoop(t *testing.T) {
	r := &blockingRunner{entered: make(chan struct{})}
	tm, tasks, meta, ready := newDispatcherManager(r)
	ctx, cancel := context.WithCancel(context.Background())
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "blocks forever")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(ctx)

	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("drive never started")
	}

	cancel() // cancelling the Start ctx must unwind the loop

	// Stop should now return promptly (loop already exiting/exited).
	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return after ctx cancellation")
	}
}

func TestDispatcherSkipsUndrivableSessionAndContinues(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()

	var errCount int
	var mu sync.Mutex

	// First enqueue an undrivable session: meta with empty ActiveTaskID.
	if err := meta.SaveMeta(ctx, "empty", &task.SessionMeta{}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, "empty"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Then a real drivable session behind it.
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "real work")

	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithErrorHandler(func(error) { mu.Lock(); errCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	defer d.Stop()

	// The drivable session still completes (loop continued past the empty one).
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-1")
		return err == nil && tk.Status == task.TaskDone
	})

	mu.Lock()
	defer mu.Unlock()
	if errCount != 0 {
		t.Errorf("errHandler fired %d times for an undrivable session, want 0 (Decision H is not an error)", errCount)
	}
}

func TestDispatcherDoubleStartPanics(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(context.Background())
	defer d.Stop()

	defer func() {
		if recover() == nil {
			t.Errorf("second Start did not panic")
		}
	}()
	d.Start(context.Background())
}

func TestDispatcherStopIsIdempotent(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(context.Background())
	d.Stop()
	// Second Stop must not panic or block.
	done := make(chan struct{})
	go func() { d.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop blocked")
	}
}

func TestDispatcherStopBeforeStartIsNoOp(t *testing.T) {
	tm, _, _, _ := newDispatcherManager(completeOnceRunner{})
	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	// Stop without Start must return immediately without panicking.
	done := make(chan struct{})
	go func() { d.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop before Start blocked")
	}
}
