package taskmanager

import (
	"context"
	"encoding/json"
	"time"

	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/task"
)

// NotifyBridge pre-wires a NotificationStore into the Dispatcher's two callback
// seams. Use it as:
//
//	parked, terminal := taskmanager.NotifyBridge(store)
//	d := taskmanager.NewDispatcher(tm,
//	    taskmanager.WithNotifier(parked),
//	    taskmanager.WithCompletionNotifier(terminal),
//	)
//
// parked records a NotificationParked whose Body is the first pending ask_user
// question's text (best-effort; empty when not extractable). terminal maps
// done/failed/cancelled to the matching kind with task.Result(t) as the Body;
// it ignores non-terminal statuses. Both target the task's ParentSessionID when
// set (the chat session that spawned it), else the task's own SessionID.
//
// The callbacks run on the dispatch loop's goroutine with no context or error
// return (fire-and-forget, mirroring WithNotifier), so Append runs under
// context.Background() and a store error is dropped — the task itself stays
// durable in its TaskStore either way. Panics if store is nil.
func NotifyBridge(store NotificationStore) (parked func(*task.Task), terminal func(*task.Task)) {
	if store == nil {
		panic("taskmanager: NotifyBridge requires a non-nil NotificationStore")
	}
	record := func(t *task.Task, kind NotificationKind, body string) {
		_ = store.Append(context.Background(), &Notification{
			ID:        newNotificationID(),
			SessionID: notifyTarget(t),
			TaskID:    t.ID,
			Kind:      kind,
			Title:     t.Title,
			Body:      body,
			CreatedAt: time.Now().UTC(),
		})
	}
	parked = func(t *task.Task) {
		if t == nil {
			return
		}
		record(t, NotificationParked, pendingQuestion(t))
	}
	terminal = func(t *task.Task) {
		if t == nil {
			return
		}
		var kind NotificationKind
		switch t.Status {
		case task.TaskDone:
			kind = NotificationDone
		case task.TaskFailed:
			kind = NotificationFailed
		case task.TaskCancelled:
			kind = NotificationCancelled
		default:
			return // not terminal; nothing to record
		}
		record(t, kind, task.Result(t))
	}
	return parked, terminal
}

// notifyTarget returns the session a task's notifications surface in: the
// parent chat session when the task was spawned from one, else its own session.
func notifyTarget(t *task.Task) string {
	if t.ParentSessionID != "" {
		return t.ParentSessionID
	}
	return t.SessionID
}

// pendingQuestion best-effort extracts questions[0].text from a parked task's
// first pending ask_user call; returns "" when it cannot (no pending calls,
// non-ask input shape, or no questions). Mirrors firstQuestion in
// examples/task-lifecycle-live.
func pendingQuestion(t *task.Task) string {
	if len(t.Pending) == 0 {
		return ""
	}
	var req ask.Request
	if err := json.Unmarshal(t.Pending[0].Input, &req); err != nil {
		return ""
	}
	if len(req.Questions) == 0 {
		return ""
	}
	return req.Questions[0].Text
}
