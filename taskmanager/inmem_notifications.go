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

// PeekFor returns the session's pending notifications in append order without
// removing them. An unknown session yields an empty slice and no error.
func (s *InMemoryNotificationStore) PeekFor(_ context.Context, sessionID string) ([]*Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.m[sessionID]
	out := make([]*Notification, len(ns))
	for i, n := range ns {
		cp := *n
		out[i] = &cp
	}
	return out, nil
}

// Ack removes the identified notifications for the session. Unknown ids are
// ignored; an emptied session is deleted from the map.
func (s *InMemoryNotificationStore) Ack(_ context.Context, sessionID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := s.m[sessionID]
	if len(ns) == 0 || len(ids) == 0 {
		return nil
	}
	acked := make(map[string]bool, len(ids))
	for _, id := range ids {
		acked[id] = true
	}
	kept := ns[:0]
	for _, n := range ns {
		if !acked[n.ID] {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		delete(s.m, sessionID)
		return nil
	}
	s.m[sessionID] = kept
	return nil
}
