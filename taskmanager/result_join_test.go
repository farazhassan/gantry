package taskmanager

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

// newJoinManager wires a runner into a real Driver + in-memory stores, returning
// the ready queue so tests can seed and inspect cross-session work. Task ids
// mint deterministically as task-1, task-2, ... (single-threaded tests only).
func newJoinManager(r task.Runner, opts ...Option) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n := 0
	all := append([]Option{WithIDFunc(func() string {
		n++
		return "task-" + string(rune('0'+n))
	})}, opts...)
	tm := NewTaskManager(driver, tasks, meta, ready, all...)
	return tm, tasks, meta, ready
}

// seedDetachedChild persists a pending child task in its own session, linked to
// the given parent, and enqueues the child's session on the ready queue — the
// state StartDetachedSession leaves behind for a spawn_session child once plan
// 03 stamps the parent linkage at spawn time.
func seedDetachedChild(t *testing.T, tasks task.TaskStore, meta MetaStore, ready *InMemoryReadyQueue, childSID, childTID, parentSID, parentTID string) {
	t.Helper()
	ctx := context.Background()
	child := &task.Task{
		ID:              childTID,
		SessionID:       childSID,
		ParentSessionID: parentSID,
		ParentTaskID:    parentTID,
		Goal:            "child goal",
		Status:          task.TaskPending,
		CreatedAt:       time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, child); err != nil {
		t.Fatalf("SaveTask(child): %v", err)
	}
	if err := meta.SaveMeta(ctx, childSID, &task.SessionMeta{
		TaskRefs:     []task.TaskRef{{ID: childTID, Status: task.TaskPending, CreatedAt: child.CreatedAt}},
		ActiveTaskID: childTID,
	}); err != nil {
		t.Fatalf("SaveMeta(child): %v", err)
	}
	if err := ready.Enqueue(ctx, childSID); err != nil {
		t.Fatalf("Enqueue(child session): %v", err)
	}
}

// lastMessage returns the final Working message, failing the test when empty.
func lastMessage(t *testing.T, tk *task.Task) gantry.Message {
	t.Helper()
	if len(tk.Working) == 0 {
		t.Fatalf("task %q has empty Working", tk.ID)
	}
	return tk.Working[len(tk.Working)-1]
}

func TestJoinDeliversResultToAskParkedParent(t *testing.T) {
	// Parent suspends on ask_user (Pending set); the child completes; the join
	// appends the result to the parent's Working, leaves status and Pending
	// untouched, and does NOT wake the parent (the message rides the next
	// human resume).
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),                    // parent run -> awaiting_input
		complete("the child result"), // child run -> done
	}}
	tm, tasks, meta, ready := newJoinManager(r)
	ctx := context.Background()

	parent, err := tm.StartTask(ctx, "parent-s", "parent goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if parent.Status != task.TaskAwaitingInput {
		t.Fatalf("parent status = %v, want TaskAwaitingInput", parent.Status)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", parent.ID)

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}
	if driven.ID != "child-t" || driven.Status != task.TaskDone {
		t.Fatalf("driven = (%q,%v), want (child-t, TaskDone)", driven.ID, driven.Status)
	}

	got, _ := tasks.LoadTask(ctx, parent.ID)
	if got.Status != task.TaskAwaitingInput {
		t.Errorf("parent status = %v, want TaskAwaitingInput (untouched)", got.Status)
	}
	if len(got.Pending) != 1 {
		t.Errorf("parent Pending len = %d, want 1 (untouched)", len(got.Pending))
	}
	msg := lastMessage(t, got)
	if msg.Role != gantry.RoleUser {
		t.Errorf("joined message role = %v, want RoleUser", msg.Role)
	}
	if msg.Content != "[subtask child-t done] the child result" {
		t.Errorf("joined message = %q, want \"[subtask child-t done] the child result\"", msg.Content)
	}
	if sid, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue has %q, want empty (a parked parent is never woken)", sid)
	}
}

func TestJoinDeliversResultToRejectionParkedParent(t *testing.T) {
	// A rejection-cap park is awaiting_input with NO Pending. The join appends
	// the message and leaves the status untouched; no wake — the appended
	// message rides the next ResumeTask.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("the child result"), // child run -> done
	}}
	tm, tasks, meta, ready := newJoinManager(r)
	ctx := context.Background()

	parent := &task.Task{
		ID:        "parent-t",
		SessionID: "parent-s",
		Goal:      "parent goal",
		Status:    task.TaskAwaitingInput, // parked with Pending nil = rejection cap
		Working:   []gantry.Message{{Role: gantry.RoleUser, Content: "parent goal"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, parent); err != nil {
		t.Fatalf("SaveTask(parent): %v", err)
	}
	if err := meta.SaveMeta(ctx, "parent-s", &task.SessionMeta{
		TaskRefs:     []task.TaskRef{{ID: "parent-t", Status: task.TaskAwaitingInput}},
		ActiveTaskID: "parent-t",
	}); err != nil {
		t.Fatalf("SaveMeta(parent): %v", err)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", "parent-t")

	if _, ok, err := tm.RunNextReady(ctx); err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}

	got, _ := tasks.LoadTask(ctx, "parent-t")
	if got.Status != task.TaskAwaitingInput || len(got.Pending) != 0 {
		t.Errorf("parent = (%v, %d pending), want (TaskAwaitingInput, 0) untouched", got.Status, len(got.Pending))
	}
	if msg := lastMessage(t, got); msg.Content != "[subtask child-t done] the child result" {
		t.Errorf("joined message = %q", msg.Content)
	}
	if sid, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue has %q, want empty (parked parent not woken)", sid)
	}
}

func TestJoinFailedChildDeliversFailedVerdict(t *testing.T) {
	// A child that ends TaskFailed on a clean (non-error) drive joins with the
	// "failed" verdict; with no assistant answer, the summary falls back to
	// "(no output)".
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(), // parent run -> awaiting_input
		fail(),    // child run -> failed (DoneError; no Go error)
	}}
	tm, tasks, meta, ready := newJoinManager(r)
	ctx := context.Background()

	parent, err := tm.StartTask(ctx, "parent-s", "parent goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", parent.ID)

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}
	if driven.Status != task.TaskFailed {
		t.Fatalf("driven status = %v, want TaskFailed", driven.Status)
	}
	got, _ := tasks.LoadTask(ctx, parent.ID)
	if msg := lastMessage(t, got); msg.Content != "[subtask child-t failed] (no output)" {
		t.Errorf("joined message = %q, want \"[subtask child-t failed] (no output)\"", msg.Content)
	}
}

func TestJoinDropsResultForTerminalParent(t *testing.T) {
	// Parent completes before the child. The child's result has no live
	// consumer: the orphan handler fires and the parent's Working is untouched.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("parent done"), // parent run -> done
		complete("child done"),  // child run -> done
	}}
	var orphaned []*task.Task
	tm, tasks, meta, ready := newJoinManager(r, WithOrphanResultHandler(func(c *task.Task) {
		orphaned = append(orphaned, c)
	}))
	ctx := context.Background()

	parent, err := tm.StartTask(ctx, "parent-s", "parent goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if parent.Status != task.TaskDone {
		t.Fatalf("parent status = %v, want TaskDone", parent.Status)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", parent.ID)

	if _, ok, err := tm.RunNextReady(ctx); err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}
	if len(orphaned) != 1 || orphaned[0].ID != "child-t" {
		t.Fatalf("orphaned = %+v, want exactly the child", orphaned)
	}
	got, _ := tasks.LoadTask(ctx, parent.ID)
	if msg := lastMessage(t, got); strings.Contains(msg.Content, "[subtask") {
		t.Errorf("terminal parent Working gained a subtask message: %q", msg.Content)
	}
}

func TestJoinDropsResultForMissingParent(t *testing.T) {
	// The recorded parent task does not exist: orphan handler fires, no error.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("child done"), // child run -> done
	}}
	var orphaned []*task.Task
	tm, tasks, meta, ready := newJoinManager(r, WithOrphanResultHandler(func(c *task.Task) {
		orphaned = append(orphaned, c)
	}))
	ctx := context.Background()

	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "ghost-s", "ghost-t")

	if _, ok, err := tm.RunNextReady(ctx); err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}
	if len(orphaned) != 1 || orphaned[0].ID != "child-t" {
		t.Errorf("orphaned = %+v, want exactly the child", orphaned)
	}
}

func TestJoinWakesIdlePendingParent(t *testing.T) {
	// Defensive branch: the parent session is idle (ActiveTaskID == "") and the
	// parent task is pending — resumable. The join re-activates it and enqueues
	// the parent session so the dispatcher picks it up.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("child done"), // child run -> done
	}}
	tm, tasks, meta, ready := newJoinManager(r)
	ctx := context.Background()

	parent := &task.Task{
		ID:        "parent-t",
		SessionID: "parent-s",
		Goal:      "parent goal",
		Status:    task.TaskPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, parent); err != nil {
		t.Fatalf("SaveTask(parent): %v", err)
	}
	if err := meta.SaveMeta(ctx, "parent-s", &task.SessionMeta{
		TaskRefs: []task.TaskRef{{ID: "parent-t", Status: task.TaskPending}},
		// ActiveTaskID deliberately "" — idle session.
	}); err != nil {
		t.Fatalf("SaveMeta(parent): %v", err)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", "parent-t")

	if _, ok, err := tm.RunNextReady(ctx); err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}

	sm, _ := meta.LoadMeta(ctx, "parent-s")
	if sm.ActiveTaskID != "parent-t" {
		t.Errorf("ActiveTaskID = %q, want parent-t (re-activated)", sm.ActiveTaskID)
	}
	sid, ok, _ := ready.Dequeue(ctx)
	if !ok || sid != "parent-s" {
		t.Errorf("ready queue = (%q, %v), want (parent-s, true) — parent woken", sid, ok)
	}
	got, _ := tasks.LoadTask(ctx, "parent-t")
	if msg := lastMessage(t, got); msg.Content != "[subtask child-t done] child done" {
		t.Errorf("joined message = %q, want \"[subtask child-t done] child done\"", msg.Content)
	}
}

func TestJoinAppendsToActiveParentWithoutWake(t *testing.T) {
	// The parent is already activated (sm.ActiveTaskID == parent.ID) with no
	// in-flight run — e.g. a detached parent still sitting on the ReadyQueue
	// awaiting its first drive, or a run lost mid-flight. The join appends the
	// message and leaves the status untouched, but must NOT enqueue: a duplicate
	// enqueue would re-drive the parent (the double-enqueue hazard).
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("the child result"), // child run -> done
	}}
	tm, tasks, meta, ready := newJoinManager(r)
	ctx := context.Background()

	parent := &task.Task{
		ID:        "parent-t",
		SessionID: "parent-s",
		Goal:      "parent goal",
		Status:    task.TaskActive,
		Working:   []gantry.Message{{Role: gantry.RoleUser, Content: "parent goal"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, parent); err != nil {
		t.Fatalf("SaveTask(parent): %v", err)
	}
	if err := meta.SaveMeta(ctx, "parent-s", &task.SessionMeta{
		TaskRefs:     []task.TaskRef{{ID: "parent-t", Status: task.TaskActive}},
		ActiveTaskID: "parent-t",
	}); err != nil {
		t.Fatalf("SaveMeta(parent): %v", err)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", "parent-t")

	if _, ok, err := tm.RunNextReady(ctx); err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}

	got, _ := tasks.LoadTask(ctx, "parent-t")
	if got.Status != task.TaskActive {
		t.Errorf("parent status = %v, want TaskActive (untouched)", got.Status)
	}
	if msg := lastMessage(t, got); msg.Content != "[subtask child-t done] the child result" {
		t.Errorf("joined message = %q, want \"[subtask child-t done] the child result\"", msg.Content)
	}
	if sid, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue has %q, want empty (already-activated parent never re-enqueued)", sid)
	}
}

func TestJoinFollowOnSpawnStillJoinsDetachedChild(t *testing.T) {
	// The detached child spawns a same-session create_task follow-on before
	// completing. drive() returns the LAST task the FIFO drain drove — the
	// follow-on — so the join must key off the ORIGINALLY-ACTIVATED child: its
	// result reaches the cross-session parent, and the follow-on (a same-session
	// child whose parent is terminal by construction of the drain — plan 11's
	// mailbox territory) fires no orphan.
	r := &spawningRunner{
		tool:      NewCreateTaskTool(),
		spawnReqs: []spawnReq{{goal: "follow-on work"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("the child result"), // child run -> done (spawns the follow-on)
			complete("follow-on done"),   // follow-on drains in the child's session
		},
	}
	var orphaned []*task.Task
	tm, tasks, meta, ready := newJoinManager(r, WithOrphanResultHandler(func(c *task.Task) {
		orphaned = append(orphaned, c)
	}))
	ctx := context.Background()

	parent := &task.Task{
		ID:        "parent-t",
		SessionID: "parent-s",
		Goal:      "parent goal",
		Status:    task.TaskAwaitingInput, // parked; spawningRunner spawns on its FIRST Resume, so the child must be the first drive
		Working:   []gantry.Message{{Role: gantry.RoleUser, Content: "parent goal"}},
		CreatedAt: time.Now().UTC(),
	}
	if err := tasks.SaveTask(ctx, parent); err != nil {
		t.Fatalf("SaveTask(parent): %v", err)
	}
	if err := meta.SaveMeta(ctx, "parent-s", &task.SessionMeta{
		TaskRefs:     []task.TaskRef{{ID: "parent-t", Status: task.TaskAwaitingInput}},
		ActiveTaskID: "parent-t",
	}); err != nil {
		t.Fatalf("SaveMeta(parent): %v", err)
	}
	seedDetachedChild(t, tasks, meta, ready, "child-s", "child-t", "parent-s", "parent-t")

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (true, nil)", ok, err)
	}
	if driven.ID == "child-t" {
		t.Fatalf("driven = %q; expected the drain to return the follow-on, not the activated child", driven.ID)
	}
	if driven.Status != task.TaskDone {
		t.Fatalf("follow-on status = %v, want TaskDone", driven.Status)
	}
	got, _ := tasks.LoadTask(ctx, "parent-t")
	if msg := lastMessage(t, got); msg.Content != "[subtask child-t done] the child result" {
		t.Errorf("joined message = %q, want the DETACHED CHILD's result, not the follow-on's", msg.Content)
	}
	if len(orphaned) != 0 {
		t.Errorf("orphaned = %+v, want none (same-session follow-on must not fire the orphan handler)", orphaned)
	}
}
