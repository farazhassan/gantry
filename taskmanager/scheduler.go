package taskmanager

import (
	"context"
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
