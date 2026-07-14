package taskmanager

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

// newAsyncManager wires a runner into a real Driver + in-memory stores with a
// deterministic id minter ("task-1", "task-2", ...), returning the ready queue
// so tests can observe StartTaskAsync's enqueue. The minter is single-threaded;
// concurrency tests build their own manager with a mutex-guarded one.
func newAsyncManager(r task.Runner) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n := 0
	tm := NewTaskManager(driver, tasks, meta, ready, WithIDFunc(func() string {
		n++
		return "task-" + string(rune('0'+n))
	}))
	return tm, tasks, meta, ready
}

func TestStartTaskAsyncNoActivePersistsActivatesEnqueues(t *testing.T) {
	// steps: nil — ANY Resume call would panic (index out of range), proving
	// StartTaskAsync returns without driving.
	r := &scriptedRunner{}
	tm, tasks, meta, ready := newAsyncManager(r)
	ctx := context.Background()

	got, err := tm.StartTaskAsync(ctx, "s1", "async goal")
	if err != nil {
		t.Fatalf("StartTaskAsync: %v", err)
	}
	if got.ID != "task-1" || got.Status != task.TaskPending || got.Goal != "async goal" {
		t.Errorf("returned task = (%q,%v,%q), want (task-1, TaskPending, async goal)", got.ID, got.Status, got.Goal)
	}
	// Persisted pending — never driven.
	tk, err := tasks.LoadTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.Status != task.TaskPending {
		t.Errorf("stored status = %v, want TaskPending (not driven)", tk.Status)
	}
	if r.calls != 0 {
		t.Errorf("runner calls = %d, want 0 (async start never drives)", r.calls)
	}
	// Meta: the task became active (none was active before).
	m, err := meta.LoadMeta(ctx, "s1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if m.ActiveTaskID != "task-1" {
		t.Errorf("ActiveTaskID = %q, want task-1", m.ActiveTaskID)
	}
	if len(m.Queue) != 0 {
		t.Errorf("Queue = %v, want empty", m.Queue)
	}
	if len(m.TaskRefs) != 1 || m.TaskRefs[0].ID != "task-1" || m.TaskRefs[0].Status != task.TaskPending {
		t.Errorf("TaskRefs = %+v, want one pending ref to task-1", m.TaskRefs)
	}
	// Ready queue: exactly one entry, s1.
	sid, ok, err := ready.Dequeue(ctx)
	if err != nil || !ok || sid != "s1" {
		t.Errorf("Dequeue = (%q,%v,%v), want (s1,true,nil)", sid, ok, err)
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("second Dequeue ok = true, want empty queue")
	}
}

func TestStartTaskAsyncBehindActiveQueues(t *testing.T) {
	// t1 (inline StartTask) suspends and stays active; the async task must queue
	// behind it, not displace it — and s1 still lands on the ready queue.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{suspend()}}
	tm, tasks, meta, ready := newAsyncManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "first goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if first.Status != task.TaskAwaitingInput {
		t.Fatalf("first status = %v, want TaskAwaitingInput", first.Status)
	}

	second, err := tm.StartTaskAsync(ctx, "s1", "second goal")
	if err != nil {
		t.Fatalf("StartTaskAsync: %v", err)
	}
	if second.ID != "task-2" || second.Status != task.TaskPending {
		t.Errorf("second = (%q,%v), want (task-2, TaskPending)", second.ID, second.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != first.ID {
		t.Errorf("ActiveTaskID = %q, want first %q (async must not displace the active task)", m.ActiveTaskID, first.ID)
	}
	if len(m.Queue) != 1 || m.Queue[0] != "task-2" {
		t.Errorf("Queue = %v, want [task-2]", m.Queue)
	}
	if len(m.TaskRefs) != 2 {
		t.Errorf("TaskRefs len = %d, want 2", len(m.TaskRefs))
	}
	tk, _ := tasks.LoadTask(ctx, "task-2")
	if tk.Status != task.TaskPending {
		t.Errorf("stored second status = %v, want TaskPending", tk.Status)
	}
	if r.calls != 1 {
		t.Errorf("runner calls = %d, want 1 (only the inline first drive)", r.calls)
	}
	sid, ok, _ := ready.Dequeue(ctx)
	if !ok || sid != "s1" {
		t.Errorf("Dequeue = (%q,%v), want (s1,true)", sid, ok)
	}
}

func TestRunNextReadyDrivesAsyncStartedTask(t *testing.T) {
	// The dispatcher path: an async-started task is driven to done by
	// RunNextReady from its own goal.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, tasks, meta, _ := newAsyncManager(r)
	ctx := context.Background()

	if _, err := tm.StartTaskAsync(ctx, "s1", "async goal"); err != nil {
		t.Fatalf("StartTaskAsync: %v", err)
	}
	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil {
		t.Fatalf("RunNextReady: %v", err)
	}
	if !ok || driven == nil || driven.ID != "task-1" || driven.Status != task.TaskDone {
		t.Fatalf("RunNextReady = (%+v,%v), want task-1 TaskDone", driven, ok)
	}
	tk, _ := tasks.LoadTask(ctx, "task-1")
	if tk.Status != task.TaskDone {
		t.Errorf("stored status = %v, want TaskDone", tk.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("meta not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
	if len(m.TaskRefs) != 1 || m.TaskRefs[0].Status != task.TaskDone {
		t.Errorf("TaskRefs = %+v, want one done ref", m.TaskRefs)
	}
}

func TestRunNextReadySkipsAwaitingInputSession(t *testing.T) {
	// t1 starts async and suspends when RunNextReady drives it. A second async
	// start re-enqueues s1. Dequeuing that entry must NOT resume the parked t1
	// (that would feed t1's GOAL to its pending ask_user call) — it must skip,
	// leaving the resume to a human ResumeTask.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),           // t1 driven via RunNextReady -> awaiting_input
		complete("t1 done"), // consumed ONLY by the human ResumeTask below
		complete("t2 done"), // t2 drained inline after t1 completes
	}}
	tm, tasks, meta, _ := newAsyncManager(r)
	ctx := context.Background()

	if _, err := tm.StartTaskAsync(ctx, "s1", "g1"); err != nil {
		t.Fatalf("StartTaskAsync t1: %v", err)
	}
	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || driven == nil || driven.Status != task.TaskAwaitingInput {
		t.Fatalf("first RunNextReady = (%+v,%v,%v), want t1 awaiting_input", driven, ok, err)
	}

	if _, err := tm.StartTaskAsync(ctx, "s1", "g2"); err != nil {
		t.Fatalf("StartTaskAsync t2: %v", err)
	}
	// The re-enqueued entry must be skipped — no drive, no resume.
	skipped, ok, err := tm.RunNextReady(ctx)
	if err != nil {
		t.Fatalf("second RunNextReady: %v", err)
	}
	if !ok {
		t.Errorf("ok = false, want true (the entry was consumed)")
	}
	if skipped != nil {
		t.Errorf("driven = %+v, want nil (awaiting task is parked for a human)", skipped)
	}
	t1, _ := tasks.LoadTask(ctx, "task-1")
	if t1.Status != task.TaskAwaitingInput {
		t.Errorf("t1 = %v, want still TaskAwaitingInput (not mis-resumed with its goal)", t1.Status)
	}
	if r.calls != 1 {
		t.Errorf("runner calls = %d, want 1 (the skip consumed no runner step)", r.calls)
	}

	// The human resume still works and drains t2 behind it.
	if _, err := tm.ResumeTask(ctx, "s1", "the real answer"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	for _, id := range []string{"task-1", "task-2"} {
		tk, _ := tasks.LoadTask(ctx, id)
		if tk.Status != task.TaskDone {
			t.Errorf("task %q = %v, want TaskDone", id, tk.Status)
		}
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("meta dirty: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}
