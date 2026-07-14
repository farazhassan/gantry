package taskmanager

import (
	"context"
	"sync"
	"time"

	"github.com/farazhassan/gantry/task"
)

// Dispatcher automatically consumes a TaskManager's ReadyQueue on a background
// goroutine, driving spawned cross-session work via RunNextReady. It owns the
// only goroutine in the package; the TaskManager it wraps stays synchronous and
// goroutine-free. The caller-driven RunNextReady primitive remains usable
// directly for manual or test control.
type Dispatcher struct {
	tm         *TaskManager
	interval   time.Duration
	errHandler func(error)
	notifier   func(*task.Task)
	workers    int

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
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
func WithErrorHandler(f func(error)) DispatcherOption {
	return func(dp *Dispatcher) { dp.errHandler = f }
}

// WithNotifier sets a callback invoked when a dispatched task parks at
// awaiting_input with no human attached. The callback receives the parked task
// (carrying SessionID, Goal, Title, and the unfulfilled ask_user calls in
// Pending) so an external bridge can surface the question. Default is a no-op.
//
// Fire-and-forget: it returns no error and runs synchronously on the dispatch
// loop's goroutine, so it must be quick and non-blocking (hand off to a channel
// or a separate goroutine for slow work). This mirrors WithErrorHandler.
func WithNotifier(f func(*task.Task)) DispatcherOption {
	return func(dp *Dispatcher) { dp.notifier = f }
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
		tm:         tm,
		interval:   time.Second,
		errHandler: func(error) {},
		notifier:   func(*task.Task) {},
		workers:    1,
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

// Start launches the dispatch loop on a new goroutine and returns immediately.
// Cancelling ctx is equivalent to calling Stop. Calling Start more than once
// panics.
func (d *Dispatcher) Start(ctx context.Context) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		panic("taskmanager: Dispatcher.Start called more than once")
	}
	d.started = true
	runCtx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.done = make(chan struct{})
	go d.loop(runCtx)
}

// Stop cancels any in-flight drive and blocks until the loop goroutine exits.
// It is idempotent and safe to call after the Start ctx has been cancelled. A
// Stop before Start is a no-op.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	if !d.started || d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	cancel := d.cancel
	done := d.done
	d.mu.Unlock()

	cancel()
	<-done
}

// loop is the single-worker dispatch loop. It drains the ready queue while work
// is available and waits one interval when the queue is empty (or a dequeue
// errored), exiting when ctx is cancelled.
func (d *Dispatcher) loop(ctx context.Context) {
	defer close(d.done)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		t, ok, err := d.tm.RunNextReady(ctx)
		if err != nil {
			d.errHandler(err)
		} else if t != nil && t.Status == task.TaskAwaitingInput {
			// Headless park: a task with no human attached is waiting for input.
			// Surface it via the notifier. This fires ONLY on a clean park, not on
			// terminal completion (t.Status != awaiting_input), the Decision-H
			// undrivable skip (t == nil), or an errored drive (err != nil routes to
			// errHandler and skips this branch).
			d.notifier(t)
		}
		if ok {
			// Consumed a queue item (driven, undrivable, or drive-errored);
			// try the next one immediately.
			continue
		}

		// Empty queue (or dequeue error): wait one interval or stop.
		select {
		case <-ctx.Done():
			return
		case <-time.After(d.interval):
		}
	}
}
