package taskmanager

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/task"
)

// askInput marshals a one-question ask.Request the way the ask_user tool
// receives it, so pendingQuestion exercises the real wire shape.
func askInput(t *testing.T, text string) json.RawMessage {
	t.Helper()
	in, err := json.Marshal(ask.Request{Questions: []ask.Question{{Header: "q", Text: text}}})
	if err != nil {
		t.Fatalf("marshal ask.Request: %v", err)
	}
	return in
}

func TestNotifyBridgeParkedWritesQuestionToOwnSession(t *testing.T) {
	store := NewInMemoryNotificationStore()
	parked, _ := NotifyBridge(store)

	tk := &task.Task{
		ID:        "t1",
		SessionID: "s1",
		Title:     "picker",
		Status:    task.TaskAwaitingInput,
		Pending:   []gantry.ToolCall{{ID: "call-1", Name: "ask_user", Input: askInput(t, "Which color do you prefer?")}},
	}
	parked(tk)

	got, err := store.DrainFor(context.Background(), "s1")
	if err != nil {
		t.Fatalf("DrainFor: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("notifications = %d, want 1", len(got))
	}
	n := got[0]
	if n.Kind != NotificationParked {
		t.Errorf("Kind = %q, want NotificationParked", n.Kind)
	}
	if n.TaskID != "t1" || n.Title != "picker" {
		t.Errorf("TaskID/Title = %q/%q, want t1/picker", n.TaskID, n.Title)
	}
	if n.Body != "Which color do you prefer?" {
		t.Errorf("Body = %q, want the pending question text", n.Body)
	}
	if n.ID == "" || n.CreatedAt.IsZero() {
		t.Errorf("ID/CreatedAt not populated: %+v", n)
	}
}

func TestNotifyBridgeTargetsParentSessionWhenSet(t *testing.T) {
	store := NewInMemoryNotificationStore()
	parked, terminal := NotifyBridge(store)
	ctx := context.Background()

	parked(&task.Task{
		ID: "c1", SessionID: "child-sess", ParentSessionID: "parent-sess",
		Status:  task.TaskAwaitingInput,
		Pending: []gantry.ToolCall{{ID: "call-1", Name: "ask_user", Input: askInput(t, "May I proceed?")}},
	})
	terminal(&task.Task{
		ID: "c2", SessionID: "child-sess", ParentSessionID: "parent-sess",
		Status:  task.TaskDone,
		Working: []gantry.Message{{Role: gantry.RoleAssistant, Content: "child summary"}},
	})

	own, _ := store.DrainFor(ctx, "child-sess")
	if len(own) != 0 {
		t.Errorf("child session received %d notifications, want 0 (parent is the target)", len(own))
	}
	parent, _ := store.DrainFor(ctx, "parent-sess")
	if len(parent) != 2 {
		t.Fatalf("parent session received %d notifications, want 2", len(parent))
	}
	if parent[0].Kind != NotificationParked || parent[1].Kind != NotificationDone {
		t.Errorf("kinds = [%q, %q], want [parked, done]", parent[0].Kind, parent[1].Kind)
	}
}

func TestNotifyBridgeTerminalKindsAndResultBody(t *testing.T) {
	store := NewInMemoryNotificationStore()
	_, terminal := NotifyBridge(store)
	ctx := context.Background()

	mk := func(id string, st task.TaskStatus) *task.Task {
		return &task.Task{
			ID: id, SessionID: "s1", Status: st,
			Working: []gantry.Message{{Role: gantry.RoleAssistant, Content: "summary for " + id}},
		}
	}
	terminal(mk("t1", task.TaskDone))
	terminal(mk("t2", task.TaskFailed))
	terminal(mk("t3", task.TaskCancelled))

	got, _ := store.DrainFor(ctx, "s1")
	if len(got) != 3 {
		t.Fatalf("notifications = %d, want 3", len(got))
	}
	wantKinds := []NotificationKind{NotificationDone, NotificationFailed, NotificationCancelled}
	for i, n := range got {
		if n.Kind != wantKinds[i] {
			t.Errorf("got[%d].Kind = %q, want %q", i, n.Kind, wantKinds[i])
		}
		wantBody := "summary for " + n.TaskID
		if n.Body != wantBody {
			t.Errorf("got[%d].Body = %q, want %q (task.Result)", i, n.Body, wantBody)
		}
	}
}

func TestNotifyBridgeTerminalIgnoresNonTerminalStatus(t *testing.T) {
	store := NewInMemoryNotificationStore()
	_, terminal := NotifyBridge(store)

	terminal(&task.Task{ID: "t1", SessionID: "s1", Status: task.TaskActive})
	got, _ := store.DrainFor(context.Background(), "s1")
	if len(got) != 0 {
		t.Errorf("non-terminal task produced %d notifications, want 0", len(got))
	}
}

func TestNotifyBridgeParkedWithoutQuestionStillRecords(t *testing.T) {
	store := NewInMemoryNotificationStore()
	parked, _ := NotifyBridge(store)

	// Rejection-cap park: awaiting_input with empty Pending (see task/driver.go).
	parked(&task.Task{ID: "t1", SessionID: "s1", Status: task.TaskAwaitingInput})
	got, _ := store.DrainFor(context.Background(), "s1")
	if len(got) != 1 {
		t.Fatalf("notifications = %d, want 1", len(got))
	}
	if got[0].Kind != NotificationParked || got[0].Body != "" {
		t.Errorf("got = %+v, want a parked notification with empty Body", got[0])
	}
}

func TestNotifyBridgeNilTaskIsNoOp(t *testing.T) {
	store := NewInMemoryNotificationStore()
	parked, terminal := NotifyBridge(store)
	parked(nil)
	terminal(nil)
	// No session to drain by name; verify both known probes are empty.
	if got, _ := store.DrainFor(context.Background(), ""); len(got) != 0 {
		t.Errorf("nil task produced %d notifications, want 0", len(got))
	}
}

func TestNotifyBridgeNilStorePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("NotifyBridge(nil) did not panic")
		}
	}()
	NotifyBridge(nil)
}
