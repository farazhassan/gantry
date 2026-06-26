package taskmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Schedule is a pending one-shot scheduled task.
type Schedule struct {
	ID     string
	Goal   string
	Title  string
	FireAt time.Time
}

// ScheduleStore persists pending one-shot schedules. Deliberately minimal,
// mirroring ReadyQueue (Add/Due/Remove, no claim/ack): a durable backend may add
// redelivery later, but the in-memory impl cannot meaningfully exercise it.
type ScheduleStore interface {
	Add(ctx context.Context, s Schedule) error
	// Due returns schedules whose FireAt <= now, ordered by FireAt (FIFO).
	Due(ctx context.Context, now time.Time) ([]Schedule, error)
	Remove(ctx context.Context, id string) error
}

// InMemoryScheduleStore is a process-local ScheduleStore backed by a map and
// mutex. A removed schedule is gone (no claim/ack).
type InMemoryScheduleStore struct {
	mu sync.Mutex
	m  map[string]Schedule
}

// NewInMemoryScheduleStore returns an empty in-memory schedule store.
func NewInMemoryScheduleStore() *InMemoryScheduleStore {
	return &InMemoryScheduleStore{m: make(map[string]Schedule)}
}

// Add stores (or overwrites) a schedule by id.
func (s *InMemoryScheduleStore) Add(_ context.Context, sc Schedule) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[sc.ID] = sc
	return nil
}

// Due returns all schedules with FireAt <= now, ordered by FireAt ascending.
func (s *InMemoryScheduleStore) Due(_ context.Context, now time.Time) ([]Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []Schedule
	for _, sc := range s.m {
		if !sc.FireAt.After(now) { // FireAt <= now
			due = append(due, sc)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].FireAt.Before(due[j].FireAt) })
	return due, nil
}

// Remove deletes a schedule by id. Removing an unknown id is a no-op.
func (s *InMemoryScheduleStore) Remove(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// newScheduleID mints a random schedule id. Falls back to a timestamp if the
// entropy source fails (never expected in practice).
func newScheduleID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sch-%d", time.Now().UnixNano())
	}
	return "sch-" + hex.EncodeToString(b[:])
}

// Scheduler fires one-shot scheduled tasks at their appointed time by creating
// detached sessions that feed the TaskManager's ReadyQueue (drained by the
// Dispatcher). It owns a single background goroutine; the TaskManager it wraps
// stays synchronous. A Scheduler feeds the queue; a Dispatcher drains it.
type Scheduler struct {
	tm         *TaskManager
	store      ScheduleStore
	interval   time.Duration
	clock      func() time.Time
	errHandler func(error)
	newID      func() string

	mu      sync.Mutex
	started bool
	stopped bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithScheduleClock overrides the clock used to evaluate due schedules. Default
// time.Now. Tests inject a controllable fake.
func WithScheduleClock(now func() time.Time) SchedulerOption {
	return func(s *Scheduler) { s.clock = now }
}

// WithSchedulePollInterval sets how long the loop waits between ticks. Default
// 1s. Must be > 0.
func WithSchedulePollInterval(d time.Duration) SchedulerOption {
	return func(s *Scheduler) { s.interval = d }
}

// WithScheduleErrorHandler sets a callback invoked with any error from Due,
// Remove, or StartDetachedSession. Default no-op. Doubles as the observability
// seam.
func WithScheduleErrorHandler(f func(error)) SchedulerOption {
	return func(s *Scheduler) { s.errHandler = f }
}

// WithScheduleIDFunc overrides the schedule-id minter (tests use a
// deterministic one).
func WithScheduleIDFunc(f func() string) SchedulerOption {
	return func(s *Scheduler) { s.newID = f }
}

// NewScheduler builds a scheduler over a TaskManager and a ScheduleStore. Panics
// if tm or store is nil, or if the poll interval is not positive.
func NewScheduler(tm *TaskManager, store ScheduleStore, opts ...SchedulerOption) *Scheduler {
	if tm == nil || store == nil {
		panic("taskmanager: NewScheduler requires non-nil TaskManager and ScheduleStore")
	}
	s := &Scheduler{
		tm:         tm,
		store:      store,
		interval:   time.Second,
		clock:      time.Now,
		errHandler: func(error) {},
		newID:      newScheduleID,
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.interval <= 0 {
		panic("taskmanager: Scheduler poll interval must be positive")
	}
	return s
}

// Schedule records a one-shot task to fire at or after `at`. Mints and returns
// the schedule id. Safe to call before or after Start.
func (s *Scheduler) Schedule(ctx context.Context, goal, title string, at time.Time) (string, error) {
	sc := Schedule{
		ID:     s.newID(),
		Goal:   goal,
		Title:  title,
		FireAt: at,
	}
	if err := s.store.Add(ctx, sc); err != nil {
		return "", err
	}
	return sc.ID, nil
}

// Start launches the schedule loop on a new goroutine and returns immediately.
// Cancelling ctx is equivalent to calling Stop. Calling Start more than once
// panics.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		panic("taskmanager: Scheduler.Start called more than once")
	}
	s.started = true
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.loop(runCtx)
}

// Stop cancels the loop and blocks until the goroutine exits. Idempotent: safe
// to call multiple times and safe to call after ctx-cancel or before Start.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()
	cancel()
	<-done
}

// loop is the scheduler's single worker goroutine. (Firing logic added in the
// next task.)
func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.interval):
		}
	}
}
