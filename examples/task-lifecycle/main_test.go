package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/task"
)

func TestRunExample(t *testing.T) {
	res, err := RunExample(context.Background())
	if err != nil {
		t.Fatalf("RunExample returned error: %v", err)
	}

	if res.Task1Status != task.TaskDone {
		t.Errorf("draft task status = %q, want %q", res.Task1Status, task.TaskDone)
	}
	if res.Task1Rejections != 1 {
		t.Errorf("draft task rejections = %d, want 1", res.Task1Rejections)
	}
	if res.ProofreadStatus != task.TaskDone {
		t.Errorf("proofread task status = %q, want %q", res.ProofreadStatus, task.TaskDone)
	}
	if res.DetachedStatus != task.TaskAwaitingInput {
		t.Errorf("detached task status = %q, want %q", res.DetachedStatus, task.TaskAwaitingInput)
	}
	if res.Notified == nil {
		t.Fatal("notifier did not fire: Notified is nil")
	}
	if res.Notified.SessionID != "schedule-posts" {
		t.Errorf("notified task SessionID = %q, want %q", res.Notified.SessionID, "schedule-posts")
	}
	if len(res.Notified.Pending) == 0 {
		t.Error("notified task has empty Pending; expected a parked ask_user call")
	}
	if res.NotifiedQuestion != "Which timezone should the posts target?" {
		t.Errorf("notified question = %q, want %q", res.NotifiedQuestion, "Which timezone should the posts target?")
	}
}
