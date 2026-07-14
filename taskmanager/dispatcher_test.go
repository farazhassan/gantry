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

func TestNewDispatcherDefaultNotifierIsSafe(t *testing.T) {
	tm := &TaskManager{}
	d := NewDispatcher(tm)
	if d.notifier == nil {
		t.Fatalf("default notifier is nil, want non-nil no-op")
	}
	// no-op notifier must be safe to call with any task (including nil).
	d.notifier(nil)
	d.notifier(&task.Task{ID: "t1"})
}

func TestWithNotifierCapturesTask(t *testing.T) {
	tm := &TaskManager{}
	var got *task.Task
	d := NewDispatcher(tm, WithNotifier(func(tk *task.Task) { got = tk }))
	if d.notifier == nil {
		t.Fatalf("notifier not set by WithNotifier")
	}
	want := &task.Task{ID: "t1", Status: task.TaskAwaitingInput}
	d.notifier(want)
	if got != want {
		t.Errorf("notifier did not capture the task: got %v, want %v", got, want)
	}
}

// parkOnceRunner parks whatever task it is given at awaiting_input by emitting an
// unfulfilled ask_user client-tool call (the shape isAskSuspension matches).
type parkOnceRunner struct{}

func (parkOnceRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	st.Done = true
	st.DoneReason = gantry.DoneClientToolCall
	st.PendingToolCalls = []gantry.ToolCall{{ID: "call-1", Name: "ask_user"}}
	return st, nil
}

func TestDispatcherNotifiesOnHeadlessPark(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(parkOnceRunner{})
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "needs input")

	var mu sync.Mutex
	var notified []*task.Task
	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithNotifier(func(tk *task.Task) {
			mu.Lock()
			notified = append(notified, tk)
			mu.Unlock()
		}),
	)
	d.Start(ctx)
	defer d.Stop()

	// The task parks at awaiting_input...
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-1")
		return err == nil && tk.Status == task.TaskAwaitingInput
	})
	// ...and the notifier fires for it.
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notified) >= 1
	})
	// Settle through several more poll cycles: a parked task is consumed from the
	// queue once and never re-enqueued, so it must fire exactly once.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if len(notified) != 1 {
		t.Fatalf("notifier fired %d times for a single headless park, want exactly 1", len(notified))
	}
	got := notified[0]
	if got.SessionID != "s1" {
		t.Errorf("notified task SessionID = %q, want %q", got.SessionID, "s1")
	}
	if len(got.Pending) == 0 {
		t.Errorf("notified task has empty Pending, want the unfulfilled ask_user call(s)")
	}
}

func TestDispatcherDoesNotNotifyOnTerminalCompletion(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "completes")

	var mu sync.Mutex
	var notifyCount int
	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithNotifier(func(*task.Task) { mu.Lock(); notifyCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	defer d.Stop()

	// Wait for the task to reach done.
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-1")
		return err == nil && tk.Status == task.TaskDone
	})
	// Give the loop a few more poll cycles to (incorrectly) fire if it would.
	time.Sleep(20 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if notifyCount != 0 {
		t.Errorf("notifier fired %d times on a completed task, want 0", notifyCount)
	}
}

func TestDispatcherDoesNotNotifyOnUndrivableSession(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()

	// Undrivable session: meta with empty ActiveTaskID -> (nil, true, nil).
	if err := meta.SaveMeta(ctx, "empty", &task.SessionMeta{}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, "empty"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// A drivable session behind it so we have a clear "loop made progress" signal.
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "real work")

	var mu sync.Mutex
	var notifyCount int
	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithNotifier(func(*task.Task) { mu.Lock(); notifyCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	defer d.Stop()

	// The drivable session completes (loop moved past the undrivable one).
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-1")
		return err == nil && tk.Status == task.TaskDone
	})

	mu.Lock()
	defer mu.Unlock()
	if notifyCount != 0 {
		t.Errorf("notifier fired %d times; want 0 (undrivable skip and completion must not notify)", notifyCount)
	}
}

func TestDispatcherDoesNotNotifyOnErroredDrive(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(&errThenCompleteRunner{})
	ctx := context.Background()

	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "will error")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "will complete")

	errs := make(chan error, 8)
	var mu sync.Mutex
	var notifyCount int
	d := NewDispatcher(tm,
		WithPollInterval(time.Millisecond),
		WithErrorHandler(func(err error) { errs <- err }),
		WithNotifier(func(*task.Task) { mu.Lock(); notifyCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	defer d.Stop()

	// First drive errors -> errHandler fires.
	select {
	case <-errs:
	case <-time.After(2 * time.Second):
		t.Fatal("error handler never fired")
	}
	// Second session drains to done (loop continued).
	waitFor(t, func() bool {
		tk, err := tasks.LoadTask(ctx, "task-2")
		return err == nil && tk.Status == task.TaskDone
	})

	mu.Lock()
	defer mu.Unlock()
	if notifyCount != 0 {
		t.Errorf("notifier fired %d times across an errored drive + a completion, want 0", notifyCount)
	}
}

func TestNewDispatcherDefaultWorkers(t *testing.T) {
	d := NewDispatcher(&TaskManager{})
	if d.workers != 1 {
		t.Errorf("default workers = %d, want 1", d.workers)
	}
}

func TestWithWorkersSetsCount(t *testing.T) {
	d := NewDispatcher(&TaskManager{}, WithWorkers(4))
	if d.workers != 4 {
		t.Errorf("workers = %d, want 4", d.workers)
	}
}

func TestWithWorkersZeroPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("WithWorkers(0) did not panic")
		}
	}()
	NewDispatcher(&TaskManager{}, WithWorkers(0))
}

func TestWithWorkersNegativePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("WithWorkers(-3) did not panic")
		}
	}()
	NewDispatcher(&TaskManager{}, WithWorkers(-3))
}

// gateRunner signals each Resume entry, then blocks until the test closes
// release (or ctx is cancelled). Because every Resume stays blocked until
// released, K buffered entry signals can only accumulate if K Resume calls are
// in flight simultaneously — a deterministic overlap proof.
type gateRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (r *gateRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	r.entered <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

// blockManyRunner signals each Resume entry on a buffered channel, then blocks
// until ctx is cancelled. Unlike blockingRunner (which closes its channel and
// is single-use), it supports many concurrent Resume calls.
type blockManyRunner struct {
	entered chan struct{}
}

func (r *blockManyRunner) Resume(ctx context.Context, _ *gantry.State) (*gantry.State, error) {
	r.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestDispatcherWorkersDriveConcurrently(t *testing.T) {
	r := &gateRunner{entered: make(chan struct{}, 3), release: make(chan struct{})}
	tm, tasks, meta, ready := newDispatcherManager(r)
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "one")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "two")
	seedReadySession(t, ctx, tasks, meta, ready, "task-3", "s3", "three")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond), WithWorkers(3))
	d.Start(ctx)
	defer d.Stop()

	// All three drives must be in flight AT THE SAME TIME: each Resume blocks
	// on release, so the third entry signal can only arrive while the first two
	// are still blocked inside their own drives.
	for i := 0; i < 3; i++ {
		select {
		case <-r.entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("drive %d never entered; only %d concurrent drive(s), want 3", i+1, i)
		}
	}

	// Release all three; every task completes to done.
	close(r.release)
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		id := id
		waitFor(t, func() bool {
			tk, err := tasks.LoadTask(ctx, id)
			return err == nil && tk.Status == task.TaskDone
		})
	}
}

func TestDispatcherStopWaitsForAllWorkers(t *testing.T) {
	r := &blockManyRunner{entered: make(chan struct{}, 3)}
	tm, tasks, meta, ready := newDispatcherManager(r)
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "blocks")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "blocks")
	seedReadySession(t, ctx, tasks, meta, ready, "task-3", "s3", "blocks")

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond), WithWorkers(3))
	d.Start(ctx)

	// All three workers are blocked inside an in-flight drive.
	for i := 0; i < 3; i++ {
		select {
		case <-r.entered:
		case <-time.After(2 * time.Second):
			t.Fatalf("worker %d never picked up a session; want 3 concurrent drives", i+1)
		}
	}

	// Stop must cancel all three in-flight drives and wait for EVERY worker
	// goroutine to exit before returning.
	stopped := make(chan struct{})
	go func() { d.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return; not all workers exited")
	}
}

func TestDispatcherSingleWorkerDrivesSerially(t *testing.T) {
	r := &gateRunner{entered: make(chan struct{}, 2), release: make(chan struct{})}
	tm, tasks, meta, ready := newDispatcherManager(r)
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-1", "s1", "one")
	seedReadySession(t, ctx, tasks, meta, ready, "task-2", "s2", "two")

	// Default worker count (1): today's single-worker semantics, pinned.
	d := NewDispatcher(tm, WithPollInterval(time.Millisecond))
	d.Start(ctx)
	defer d.Stop()

	// The first drive enters...
	select {
	case <-r.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first drive never entered")
	}
	// ...and NO second drive begins while the first is still blocked.
	select {
	case <-r.entered:
		t.Fatal("second drive entered while the first was in flight; want serial execution with 1 worker")
	case <-time.After(50 * time.Millisecond):
	}

	// Release; the single worker finishes both sessions one after the other.
	close(r.release)
	for _, id := range []string{"task-1", "task-2"} {
		id := id
		waitFor(t, func() bool {
			tk, err := tasks.LoadTask(ctx, id)
			return err == nil && tk.Status == task.TaskDone
		})
	}
}

func TestDispatcherMultiWorkerDrainsAllSessions(t *testing.T) {
	tm, tasks, meta, ready := newDispatcherManager(completeOnceRunner{})
	ctx := context.Background()
	sessions := []struct{ taskID, sid string }{
		{"task-1", "s1"}, {"task-2", "s2"}, {"task-3", "s3"}, {"task-4", "s4"}, {"task-5", "s5"},
	}
	for _, s := range sessions {
		seedReadySession(t, ctx, tasks, meta, ready, s.taskID, s.sid, "work for "+s.sid)
	}

	d := NewDispatcher(tm, WithPollInterval(time.Millisecond), WithWorkers(3))
	d.Start(ctx)
	defer d.Stop()

	for _, s := range sessions {
		id := s.taskID
		waitFor(t, func() bool {
			tk, err := tasks.LoadTask(ctx, id)
			return err == nil && tk.Status == task.TaskDone
		})
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue not empty after multi-worker drain")
	}
}

// failingMetaStore delegates to the embedded MetaStore but fails every
// LoadMeta, making any claimed session a persistently-erroring (poison) entry.
type failingMetaStore struct{ MetaStore }

func (failingMetaStore) LoadMeta(context.Context, string) (*task.SessionMeta, error) {
	return nil, errSentinel{}
}

func TestDispatcherPacesRetriesOnPersistentlyFailingSession(t *testing.T) {
	// A nacked poison entry is redelivered forever; the loop must wait one poll
	// interval between retries instead of hot-spinning on it.
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	ready := NewInMemoryReadyQueue()
	tm := NewTaskManager(driver, tasks, failingMetaStore{NewInMemoryMetaStore()}, ready)
	ctx := context.Background()
	if err := ready.Enqueue(ctx, "poison"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var mu sync.Mutex
	errCount := 0
	d := NewDispatcher(tm,
		WithPollInterval(5*time.Millisecond),
		WithErrorHandler(func(error) { mu.Lock(); errCount++; mu.Unlock() }),
	)
	d.Start(ctx)
	time.Sleep(60 * time.Millisecond)
	d.Stop()

	mu.Lock()
	defer mu.Unlock()
	if errCount < 1 {
		t.Fatalf("errHandler never fired; want at least one paced retry")
	}
	// 60ms / 5ms ≈ 12 paced cycles; a hot spin would produce thousands. The
	// bound is deliberately generous to avoid scheduler flakes.
	if errCount > 40 {
		t.Fatalf("errHandler fired %d times in 60ms; loop is hot-spinning on a nacked entry", errCount)
	}
}
