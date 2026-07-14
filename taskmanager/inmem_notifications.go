package taskmanager

import (
	"context"
	"errors"
	"sync"
)

// InMemoryNotificationStore is a process-local NotificationStore backed by a
// map and mutex. It copies on append so callers cannot mutate stored state by
// reference (mirrors InMemoryMetaStore).
type InMemoryNotificationStore struct {
	mu sync.Mutex
	m  map[string][]*Notification
}

// NewInMemoryNotificationStore returns an empty in-memory NotificationStore.
func NewInMemoryNotificationStore() *InMemoryNotificationStore {
	return &InMemoryNotificationStore{m: make(map[string][]*Notification)}
}

// Append stores a copy of n under its SessionID, behind any earlier
// notifications for the same session (FIFO).
func (s *InMemoryNotificationStore) Append(_ context.Context, n *Notification) error {
	if n == nil {
		return errors.New("taskmanager: Append requires a non-nil Notification")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *n
	s.m[n.SessionID] = append(s.m[n.SessionID], &cp)
	return nil
}

// DrainFor returns the session's pending notifications in append order and
// removes them. An unknown session yields an empty slice and no error.
func (s *InMemoryNotificationStore) DrainFor(_ context.Context, sessionID string) ([]*Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.m[sessionID]
	delete(s.m, sessionID)
	out := make([]*Notification, len(ns))
	for i, n := range ns {
		cp := *n
		out[i] = &cp
	}
	return out, nil
}
