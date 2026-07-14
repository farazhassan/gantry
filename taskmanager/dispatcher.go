package taskmanager

import (
	"context"
	"sync"
	"time"

	"github.com/farazhassan/gantry/task"
)

// Dispatcher automatically consumes a TaskManager's ReadyQueue on background
// goroutines, driving spawned cross-session work via RunNextReady. It runs a
// pool of identical worker loops (WithWorkers; default 1). RunNextReady is safe
// from N goroutines — each dequeue yields a distinct session id and therefore a
// distinct per-session lock — so workers never contend on the same session. The
// TaskManager it wraps stays synchronous and goroutine-free. The caller-driven
// RunNextReady primitive remains usable directly for manual or test control.
type Dispatcher struct {
	tm                 *TaskManager
	interval           time.Duration
	errHandler         func(error)
	notifier           func(*task.Task)
	completionNotifier func(*task.Task)
	workers            int

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// DispatcherOption configures a Dispatcher.
type DispatcherOption func(*Dispatcher)

// WithPollInterval sets how long the loop waits before re-polling when the queue
// is empty (or a dequeue errored). Must be > 0. Default 1s.
func WithPollInterval(d time.Duration) DispatcherOption {
	return func(dp *Dispatcher) { dp.interval = d }
}

// WithErrorHandler sets a callback invoked with any error returned by
// RunNextReady. Default is a no-op. Doubles as the observability seam.
//
// Concurrency: with WithWorkers(n > 1) the callback may fire concurrently from
// multiple worker goroutines, so it must be safe for concurrent use (guard any
// shared state). It runs synchronously on a worker loop's goroutine, so it must
// also be quick and non-blocking.
func WithErrorHandler(f func(error)) DispatcherOption {
	return func(dp *Dispatcher) { dp.errHandler = f }
}

// WithNotifier sets a callback invoked when a dispatched task parks at
// awaiting_input with no human attached. The callback receives the parked task
// (carrying SessionID, Goal, Title, and the unfulfilled ask_user calls in
// Pending) so an external bridge can surface the question. Default is a no-op.
//
// Fire-and-forget: it returns no error and runs synchronously on a worker
// loop's goroutine, so it must be quick and non-blocking (hand off to a channel
// or a separate goroutine for slow work). Concurrency: with WithWorkers(n > 1)
// the callback may fire concurrently from multiple worker goroutines, so it
// must be safe for concurrent use. This mirrors WithErrorHandler.
func WithNotifier(f func(*task.Task)) DispatcherOption {
	return func(dp *Dispatcher) { dp.notifier = f }
}

// WithCompletionNotifier sets a callback invoked when a dispatched task reaches
// a terminal status (done, failed, or cancelled) on a clean drive. The callback
// receives the finished task (carrying SessionID, ParentSessionID, Title,
// Status, and Working — task.Result summarizes the answer) so an external
// bridge can record the outcome; see NotifyBridge. Default is a no-op.
//
// Fire-and-forget: it returns no error and runs synchronously on a worker
// loop's goroutine, so it must be quick and non-blocking (hand off to a channel
// or a separate goroutine for slow work). Concurrency: with WithWorkers(n > 1)
// the callback may fire concurrently from multiple worker goroutines, so it
// must be safe for concurrent use. It mirrors WithNotifier, which fires on a
// headless park instead — the two never fire for the same drive.
func WithCompletionNotifier(f func(*task.Task)) DispatcherOption {
	return func(dp *Dispatcher) { dp.completionNotifier = f }
}

// WithWorkers sets how many dispatch loop goroutines Start launches. Each
// worker independently calls RunNextReady, so up to n dequeued sessions drive
// in parallel (each dequeue yields a distinct session id and therefore a
// distinct per-session lock — see TaskManager.RunNextReady). Must be >= 1.
// Default 1 (the existing single-worker behavior).
func WithWorkers(n int) DispatcherOption {
	return func(dp *Dispatcher) { dp.workers = n }
}

// NewDispatcher builds a Dispatcher over a TaskManager. It panics if tm is nil,
// if a configured poll interval is not positive, or if a configured worker
// count is less than 1.
func NewDispatcher(tm *TaskManager, opts ...DispatcherOption) *Dispatcher {
	if tm == nil {
		panic("taskmanager: NewDispatcher requires a non-nil TaskManager")
	}
	d := &Dispatcher{
		tm:                 tm,
		interval:           time.Second,
		errHandler:         func(error) {},
		notifier:           func(*task.Task) {},
		completionNotifier: func(*task.Task) {},
		workers:            1,
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.interval <= 0 {
		panic("taskmanager: Dispatcher poll interval must be positive")
	}
	if d.workers < 1 {
		panic("taskmanager: Dispatcher worker count must be at least 1")
	}
	return d
}

// Start launches the worker pool (one loop goroutine per configured worker) and
// returns immediately. Cancelling ctx is equivalent to calling Stop. Calling
// Start more than once panics.
func (d *Dispatcher) Start(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		panic("taskmanager: Dispatcher.Start called more than once")
	}
	d.started = true
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.wg.Add(d.workers)
	for i := 0; i < d.workers; i++ {
		go d.loop(runCtx)
	}
}

// Stop cancels any in-flight drives and blocks until every worker goroutine
// exits. It is idempotent and safe to call after the Start ctx has been
// cancelled. A Stop before Start is a no-op.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.started || d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	cancel := d.cancel
	d.mu.Unlock()

	cancel()
	d.wg.Wait()
}

// loop is one worker's dispatch loop. It drains the ready queue while work is
// available, waits one interval when the queue is empty, and ALSO waits one
// interval after an errored claim — the error path nacks and redelivers the
// entry (Decision L), so continuing immediately would hot-spin on a
// persistently-failing head. It exits when ctx is cancelled. With
// WithWorkers(n), n identical loops run concurrently against the shared
// RunNextReady.
func (d *Dispatcher) loop(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		t, ok, err := d.tm.RunNextReady(ctx)
		if err != nil {
			d.errHandler(err)
			// Fall through to the interval wait: the errored claim was nacked
			// and redelivered, so pacing the retry is what prevents a spin.
		} else {
			if t != nil && t.Status == task.TaskAwaitingInput {
				// Headless park: a task with no human attached is waiting for
				// input. Surface it via the notifier. This fires ONLY on a
				// clean park — not terminals, undrivable skips (t == nil), or
				// errored drives (routed to errHandler above).
				d.notifier(t)
			} else if t != nil && t.Status.IsTerminal() {
				// Clean terminal drive (done/failed/cancelled): surface the
				// outcome via the completion notifier. Same exclusions as above:
				// errored drives go to errHandler only, undrivable skips
				// (t == nil) fire nothing.
				d.completionNotifier(t)
			}
			if ok {
				// Consumed a queue item cleanly; try the next one immediately.
				continue
			}
		}

		// Empty queue or errored claim: wait one interval or stop.
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.interval):
		}
	}
}
