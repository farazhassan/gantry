package task

import (
	"context"
	"errors"
	"sort"
	"sync"

	"github.com/farazhassan/gantry"
)

// InMemoryStore is a process-local TaskStore. Useful for tests and single-host
// development; not suitable for production resume across processes. It is safe
// for concurrent use and returns independent copies on load.
type InMemoryStore struct {
	mu    sync.Mutex
	tasks map[string]*Task
}

// NewInMemory returns an empty in-memory TaskStore.
func NewInMemory() *InMemoryStore {
	return &InMemoryStore{tasks: map[string]*Task{}}
}

// SaveTask stores an independent copy of t keyed by t.ID. A nil task or empty
// ID is rejected (an upstream bug).
func (s *InMemoryStore) SaveTask(_ context.Context, t *Task) error {
	if t == nil {
		return errors.New("task: SaveTask requires a non-nil task")
	}
	if t.ID == "" {
		return errors.New("task: SaveTask requires a non-empty ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[t.ID] = cloneTask(t)
	return nil
}

// LoadTask returns an independent copy of the stored task, or a wrapped
// ErrNotFound if no task exists for id.
func (s *InMemoryStore) LoadTask(_ context.Context, id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return nil, errWrap(ErrNotFound, id)
	}
	return cloneTask(t), nil
}

// ListBySession returns the refs of all tasks owned by sessionID, ordered by
// CreatedAt ascending. An unknown session yields an empty slice (not an error).
func (s *InMemoryStore) ListBySession(_ context.Context, sessionID string) ([]TaskRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var refs []TaskRef
	for _, t := range s.tasks {
		if t.SessionID != sessionID {
			continue
		}
		refs = append(refs, TaskRef{
			ID:        t.ID,
			Title:     t.Title,
			Status:    t.Status,
			CreatedAt: t.CreatedAt,
		})
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].CreatedAt.Equal(refs[j].CreatedAt) {
			return refs[i].ID < refs[j].ID
		}
		return refs[i].CreatedAt.Before(refs[j].CreatedAt)
	})
	return refs, nil
}

// cloneTask deep-copies the mutable parts of a Task so the store and its callers
// never share backing arrays (plan steps, working messages) or step Meta maps.
func cloneTask(t *Task) *Task {
	c := *t
	if t.Plan != nil {
		p := *t.Plan
		p.Steps = cloneSteps(t.Plan.Steps)
		c.Plan = &p
	}
	if t.Working != nil {
		c.Working = make([]gantry.Message, len(t.Working))
		copy(c.Working, t.Working)
	}
	return &c
}

// cloneSteps returns a deep copy of a plan's steps. The Steps slice and each
// step's Meta map get fresh backing storage so the store and its callers never
// share mutable state. Meta values themselves are copied by reference (a
// one-level clone), matching how callers treat Meta entries as immutable once
// set; the goal is only to prevent map-level mutation from crossing the
// isolation boundary. A nil input yields a nil result.
func cloneSteps(src []gantry.PlanStep) []gantry.PlanStep {
	if src == nil {
		return nil
	}
	out := make([]gantry.PlanStep, len(src))
	copy(out, src)
	for i := range src {
		if src[i].Meta != nil {
			m := make(map[string]any, len(src[i].Meta))
			for k, v := range src[i].Meta {
				m[k] = v
			}
			out[i].Meta = m
		}
	}
	return out
}

// Compile-time check.
var _ TaskStore = (*InMemoryStore)(nil)
