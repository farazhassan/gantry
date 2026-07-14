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

func TestInMemoryNotificationsAppendDrainFIFO(t *testing.T) {
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

	got, err := store.DrainFor(ctx, "s1")
	if err != nil {
		t.Fatalf("DrainFor: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("drain len = %d, want 2", len(got))
	}
	if got[0].ID != "n1" || got[1].ID != "n2" {
		t.Errorf("order = [%q, %q], want FIFO [n1, n2]", got[0].ID, got[1].ID)
	}
	if got[0].Kind != NotificationDone || got[0].Body != "b1" || got[0].TaskID != "t1" {
		t.Errorf("got[0] = %+v, want the full n1 record", got[0])
	}
	// Drain clears the session's buffer.
	again, err := store.DrainFor(ctx, "s1")
	if err != nil {
		t.Fatalf("second DrainFor: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second drain len = %d, want 0 (buffer cleared)", len(again))
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

	got1, _ := store.DrainFor(ctx, "s1")
	if len(got1) != 1 || got1[0].ID != "a" {
		t.Errorf("s1 drain = %+v, want only [a]", got1)
	}
	// s2 must be untouched by s1's drain.
	got2, _ := store.DrainFor(ctx, "s2")
	if len(got2) != 1 || got2[0].ID != "b" {
		t.Errorf("s2 drain = %+v, want only [b]", got2)
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

	got, _ := store.DrainFor(ctx, "s1")
	if len(got) != 1 || got[0].Body != "original" {
		t.Errorf("store corrupted by caller mutation: %+v", got)
	}
}

func TestInMemoryNotificationsDrainUnknownSessionIsEmpty(t *testing.T) {
	store := NewInMemoryNotificationStore()
	got, err := store.DrainFor(context.Background(), "never-seen")
	if err != nil {
		t.Fatalf("DrainFor: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("drain of unknown session = %+v, want empty", got)
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
