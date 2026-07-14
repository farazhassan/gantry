package taskmanager

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNotificationKindValues(t *testing.T) {
	cases := map[NotificationKind]string{
		NotificationParked:    "parked",
		NotificationDone:      "done",
		NotificationFailed:    "failed",
		NotificationCancelled: "cancelled",
	}
	for kind, want := range cases {
		if string(kind) != want {
			t.Errorf("kind = %q, want %q", string(kind), want)
		}
	}
}

func TestInMemoryNotificationsPeekFIFOAndNonDestructive(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	n1 := &Notification{ID: "n1", SessionID: "s1", TaskID: "t1", Kind: NotificationDone, Title: "first", Body: "b1", CreatedAt: time.Now().UTC()}
	n2 := &Notification{ID: "n2", SessionID: "s1", TaskID: "t2", Kind: NotificationFailed, Title: "second", Body: "b2", CreatedAt: time.Now().UTC()}
	if err := store.Append(ctx, n1); err != nil {
		t.Fatalf("Append n1: %v", err)
	}
	if err := store.Append(ctx, n2); err != nil {
		t.Fatalf("Append n2: %v", err)
	}

	got, err := store.PeekFor(ctx, "s1")
	if err != nil {
		t.Fatalf("PeekFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("peek len = %d, want 2", len(got))
	}
	if got[0].ID != "n1" || got[1].ID != "n2" {
		t.Errorf("order = [%q, %q], want FIFO [n1, n2]", got[0].ID, got[1].ID)
	}
	if got[0].Kind != NotificationDone || got[0].Body != "b1" || got[0].TaskID != "t1" {
		t.Errorf("got[0] = %+v, want the full n1 record", got[0])
	}
	// Peek is non-destructive: a second peek sees the same records.
	again, err := store.PeekFor(ctx, "s1")
	if err != nil {
		t.Fatalf("second PeekFor: %v", err)
	}
	if len(again) != 2 {
		t.Errorf("second peek len = %d, want 2 (peek must not remove)", len(again))
	}
}

func TestInMemoryNotificationsAckRemovesOnlyGivenIDs(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	for _, id := range []string{"n1", "n2", "n3"} {
		if err := store.Append(ctx, &Notification{ID: id, SessionID: "s1", Kind: NotificationDone}); err != nil {
			t.Fatalf("Append %s: %v", id, err)
		}
	}

	if err := store.Ack(ctx, "s1", []string{"n1", "n3"}); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	left, _ := store.PeekFor(ctx, "s1")
	if len(left) != 1 || left[0].ID != "n2" {
		t.Errorf("after partial ack, left = %+v, want only [n2]", left)
	}
	// Acking the rest clears the session's buffer.
	if err := store.Ack(ctx, "s1", []string{"n2"}); err != nil {
		t.Fatalf("Ack n2: %v", err)
	}
	empty, _ := store.PeekFor(ctx, "s1")
	if len(empty) != 0 {
		t.Errorf("after full ack, peek len = %d, want 0", len(empty))
	}
}

func TestInMemoryNotificationsAckUnknownIsNoOp(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	if err := store.Ack(ctx, "never-seen", []string{"nope"}); err != nil {
		t.Errorf("Ack on unknown session = %v, want nil", err)
	}
	if err := store.Append(ctx, &Notification{ID: "n1", SessionID: "s1", Kind: NotificationDone}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if err := store.Ack(ctx, "s1", []string{"unknown-id"}); err != nil {
		t.Errorf("Ack with unknown id = %v, want nil", err)
	}
	left, _ := store.PeekFor(ctx, "s1")
	if len(left) != 1 {
		t.Errorf("unknown-id ack removed something: %d left, want 1", len(left))
	}
}

func TestInMemoryNotificationsPeekIsCopy(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	if err := store.Append(ctx, &Notification{ID: "n1", SessionID: "s1", Kind: NotificationDone, Body: "original"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	got, _ := store.PeekFor(ctx, "s1")
	got[0].Body = "mutated-by-caller"

	again, _ := store.PeekFor(ctx, "s1")
	if again[0].Body != "original" {
		t.Errorf("store corrupted by mutating a peeked record: %+v", again[0])
	}
}

func TestInMemoryNotificationsSessionIsolation(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	if err := store.Append(ctx, &Notification{ID: "a", SessionID: "s1", Kind: NotificationDone}); err != nil {
		t.Fatalf("Append s1: %v", err)
	}
	if err := store.Append(ctx, &Notification{ID: "b", SessionID: "s2", Kind: NotificationParked}); err != nil {
		t.Fatalf("Append s2: %v", err)
	}

	if err := store.Ack(ctx, "s1", []string{"a"}); err != nil {
		t.Fatalf("Ack s1: %v", err)
	}
	got1, _ := store.PeekFor(ctx, "s1")
	if len(got1) != 0 {
		t.Errorf("s1 peek after ack = %+v, want empty", got1)
	}
	// s2 must be untouched by s1's ack.
	got2, _ := store.PeekFor(ctx, "s2")
	if len(got2) != 1 || got2[0].ID != "b" {
		t.Errorf("s2 peek = %+v, want only [b]", got2)
	}
}

func TestInMemoryNotificationsAppendIsCopy(t *testing.T) {
	store := NewInMemoryNotificationStore()
	ctx := context.Background()
	n := &Notification{ID: "n1", SessionID: "s1", Kind: NotificationDone, Body: "original"}
	if err := store.Append(ctx, n); err != nil {
		t.Fatalf("Append: %v", err)
	}
	n.Body = "mutated-after-append" // caller mutates its copy

	got, _ := store.PeekFor(ctx, "s1")
	if len(got) != 1 || got[0].Body != "original" {
		t.Errorf("store corrupted by caller mutation: %+v", got)
	}
}

func TestInMemoryNotificationsPeekUnknownSessionIsEmpty(t *testing.T) {
	store := NewInMemoryNotificationStore()
	got, err := store.PeekFor(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("PeekFor: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("peek of unknown session = %+v, want empty", got)
	}
}

func TestInMemoryNotificationsAppendNilIsError(t *testing.T) {
	store := NewInMemoryNotificationStore()
	if err := store.Append(context.Background(), nil); err == nil {
		t.Errorf("Append(nil) = nil error, want an error")
	}
}

func TestNewNotificationIDPrefixAndUniqueness(t *testing.T) {
	a, b := newNotificationID(), newNotificationID()
	if !strings.HasPrefix(a, "notif-") {
		t.Errorf("id = %q, want notif- prefix", a)
	}
	if a == b {
		t.Errorf("two minted ids collide: %q", a)
	}
}
