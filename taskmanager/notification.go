package taskmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// NotificationKind classifies what happened to a background task.
type NotificationKind string

const (
	NotificationParked    NotificationKind = "parked"    // task suspended at awaiting_input with no human attached
	NotificationDone      NotificationKind = "done"      // task completed (verifier-gated)
	NotificationFailed    NotificationKind = "failed"    // task terminally failed
	NotificationCancelled NotificationKind = "cancelled" // task explicitly stopped
)

// Notification is one durable "something happened to a task you care about"
// record, surfaced to the user the next time the target chat session runs (see
// components/mailbox). It is write-once: appended by a bridge on the dispatch
// loop, drained exactly once by the session that owns it.
type Notification struct {
	ID        string
	SessionID string // session to surface this in (the parent chat session)
	TaskID    string
	Kind      NotificationKind
	Title     string
	Body      string // question text for parked; result summary for terminals
	CreatedAt time.Time
}

// NotificationStore persists Notifications keyed by target session id. It
// mirrors MetaStore: a single key space, with implementations free to copy so
// callers cannot mutate stored state by reference. DrainFor is destructive —
// it returns the session's pending notifications in append order and removes
// them, so each notification is delivered at most once.
type NotificationStore interface {
	Append(ctx context.Context, n *Notification) error
	DrainFor(ctx context.Context, sessionID string) ([]*Notification, error)
}

// newNotificationID mints a random notification id. Falls back to a timestamp
// if the entropy source fails (never expected in practice). Mirrors newTaskID.
func newNotificationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("notif-%d", time.Now().UnixNano())
	}
	return "notif-" + hex.EncodeToString(b[:])
}
