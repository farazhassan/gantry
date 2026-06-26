package taskmanager

import (
	"context"
	"sync"
	"time"
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

// NewDispatcher builds a Dispatcher over a TaskManager. It panics if tm is nil
// or if a configured poll interval is not positive.
func NewDispatcher(tm *TaskManager, opts ...DispatcherOption) *Dispatcher {
	if tm == nil {
		panic("taskmanager: NewDispatcher requires a non-nil TaskManager")
	}
	d := &Dispatcher{
		tm:         tm,
		interval:   time.Second,
		errHandler: func(error) {},
	}
	for _, opt := range opts {
		opt(d)
	}
	if d.interval <= 0 {
		panic("taskmanager: Dispatcher poll interval must be positive")
	}
	return d
}
