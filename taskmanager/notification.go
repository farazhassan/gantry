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
// loop, then peeked by the owning session's run and acked once that run
// completes — so a run that fails mid-turn redelivers it (at-least-once).
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
// callers cannot mutate stored state by reference.
//
// Delivery is two-phase so a failed run cannot lose notifications: PeekFor
// reads without removing, and Ack settles the delivered ids only after the
// consumer knows the delivery stuck (components/mailbox acks at PhaseEnd,
// which a run reaches only when it completes). A run that errors after
// peeking leaves the notifications in place for redelivery — the contract is
// at-least-once, with duplicates possible only when a delivered turn's ack
// fails.
type NotificationStore interface {
	Append(ctx context.Context, n *Notification) error
	// PeekFor returns the session's pending notifications in append order
	// WITHOUT removing them. An unknown session yields an empty slice and no
	// error.
	PeekFor(ctx context.Context, sessionID string) ([]*Notification, error)
	// Ack removes the identified notifications for the session after a
	// successful delivery. Ids that are unknown (already acked, or never
	// existed) are ignored.
	Ack(ctx context.Context, sessionID string, ids []string) error
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
