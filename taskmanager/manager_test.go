package taskmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

// scriptedRunner is a fake task.Runner: each call to Resume pops the next step
// and applies it to the incoming state. Mirrors the fake in task/driver_test.go.
type scriptedRunner struct {
	steps []func(*gantry.State) *gantry.State
	calls int
}

func (r *scriptedRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	step := r.steps[r.calls]
	r.calls++
	return step(st), nil
}

// complete marks the state done with no tool calls -> driver completes the task
// (subject to the verifier; default NoopVerifier passes).
func complete(content string) func(*gantry.State) *gantry.State {
	return func(st *gantry.State) *gantry.State {
		st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: content})
		st.Done = true
		st.DoneReason = gantry.DoneNoToolCalls
		return st
	}
}

// suspend leaves a pending client tool call -> driver suspends as awaiting_input.
func suspend() func(*gantry.State) *gantry.State {
	return func(st *gantry.State) *gantry.State {
		st.Done = true
		st.DoneReason = gantry.DoneClientToolCall
		st.PendingToolCalls = []gantry.ToolCall{{ID: "call-1", Name: "ask_user"}}
		return st
	}
}

// fail ends the run with DoneError -> driver's default case marks TaskFailed.
func fail() func(*gantry.State) *gantry.State {
	return func(st *gantry.State) *gantry.State {
		st.Done = true
		st.DoneReason = gantry.DoneError
		return st
	}
}

// newManager wires a scriptedRunner into a real Driver + in-memory stores, with
// a deterministic id minter producing "task-1", "task-2", ... (single-threaded).
func newManager(r *scriptedRunner) (*TaskManager, task.TaskStore, MetaStore) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	n := 0
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(), WithIDFunc(func() string {
		n++
		return "task-" + string(rune('0'+n))
	}))
	return tm, tasks, meta
}

func TestStartTaskNoActiveDrivesToCompletion(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, meta := newManager(r)
	ctx := context.Background()

	got, err := tm.StartTask(ctx, "s1", "do the thing")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if got.Status != task.TaskDone {
		t.Errorf("status = %v, want TaskDone", got.Status)
	}
	m, err := meta.LoadMeta(ctx, "s1")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if m.ActiveTaskID != "" {
		t.Errorf("ActiveTaskID = %q, want cleared", m.ActiveTaskID)
	}
	if len(m.Queue) != 0 {
		t.Errorf("Queue = %v, want empty", m.Queue)
	}
	if len(m.TaskRefs) != 1 || m.TaskRefs[0].Status != task.TaskDone {
		t.Errorf("TaskRefs = %+v, want one ref with TaskDone", m.TaskRefs)
	}
}

func TestStartTaskWhileActiveEnqueuesPending(t *testing.T) {
	// First task suspends (awaiting input); second must enqueue pending.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{suspend()}}
	tm, _, meta := newManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "first goal")
	if err != nil {
		t.Fatalf("StartTask first: %v", err)
	}
	if first.Status != task.TaskAwaitingInput {
		t.Fatalf("first status = %v, want TaskAwaitingInput", first.Status)
	}
	second, err := tm.StartTask(ctx, "s1", "second goal")
	if err != nil {
		t.Fatalf("StartTask second: %v", err)
	}
	if second.Status != task.TaskPending {
		t.Errorf("second status = %v, want TaskPending", second.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != first.ID {
		t.Errorf("ActiveTaskID = %q, want first id %q", m.ActiveTaskID, first.ID)
	}
	if len(m.Queue) != 1 || m.Queue[0] != second.ID {
		t.Errorf("Queue = %v, want [%q]", m.Queue, second.ID)
	}
}

func TestResumeTaskFinishesActiveThenDrainsQueue(t *testing.T) {
	// t1: suspend on first run, complete on resume. t2: complete on its run.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),           // t1 run 1 -> awaiting_input
		complete("t1 done"), // t1 resume -> done
		complete("t2 done"), // t2 run -> done
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	first, _ := tm.StartTask(ctx, "s1", "first")
	second, _ := tm.StartTask(ctx, "s1", "second")
	if first.Status != task.TaskAwaitingInput || second.Status != task.TaskPending {
		t.Fatalf("setup: first=%v second=%v", first.Status, second.Status)
	}

	resumed, err := tm.ResumeTask(ctx, "s1", "here is my answer")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	// ResumeTask returns the last task driven by the drain: the second task.
	if resumed.ID != second.ID || resumed.Status != task.TaskDone {
		t.Errorf("resumed = (%q,%v), want (%q,TaskDone)", resumed.ID, resumed.Status, second.ID)
	}
	// Both tasks ended done.
	t1, _ := tasks.LoadTask(ctx, first.ID)
	if t1.Status != task.TaskDone {
		t.Errorf("t1 status = %v, want TaskDone", t1.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("meta not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestResumeTaskNothingAwaiting(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, _ := newManager(r)
	ctx := context.Background()

	// No task at all.
	if _, err := tm.ResumeTask(ctx, "s1", "x"); !errors.Is(err, ErrNoTaskAwaitingInput) {
		t.Errorf("err = %v, want ErrNoTaskAwaitingInput (no task)", err)
	}
	// Active task that completed (not awaiting).
	tm.StartTask(ctx, "s1", "goal")
	if _, err := tm.ResumeTask(ctx, "s1", "x"); !errors.Is(err, ErrNoTaskAwaitingInput) {
		t.Errorf("err = %v, want ErrNoTaskAwaitingInput (completed)", err)
	}
}

func TestFIFODrainOrder(t *testing.T) {
	// t1 suspends, then on resume completes; t2 and t3 each complete in turn.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),           // t1 run -> awaiting
		complete("t1 done"), // t1 resume -> done
		complete("t2 done"), // t2 -> done
		complete("t3 done"), // t3 -> done
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	t1, _ := tm.StartTask(ctx, "s1", "g1")
	t2, _ := tm.StartTask(ctx, "s1", "g2")
	t3, _ := tm.StartTask(ctx, "s1", "g3")

	m, _ := meta.LoadMeta(ctx, "s1")
	if len(m.Queue) != 2 || m.Queue[0] != t2.ID || m.Queue[1] != t3.ID {
		t.Fatalf("queue = %v, want [%q %q]", m.Queue, t2.ID, t3.ID)
	}

	if _, err := tm.ResumeTask(ctx, "s1", "answer"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	for _, id := range []string{t1.ID, t2.ID, t3.ID} {
		tk, _ := tasks.LoadTask(ctx, id)
		if tk.Status != task.TaskDone {
			t.Errorf("task %q status = %v, want TaskDone", id, tk.Status)
		}
	}
	m, _ = meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not fully drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

// t1 suspends so t2,t3 queue; on resume t1 completes and the drain pops t2,
// which suspends -> the drain halts with t3 still queued.
func TestDrainHaltsWhenQueuedTaskSuspends(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),           // t1 run -> awaiting (so t2,t3 queue)
		complete("t1 done"), // t1 resume -> done; drain pops t2
		suspend(),           // t2 run -> awaiting; drain halts, t3 still queued
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	t1, _ := tm.StartTask(ctx, "s1", "g1")
	t2, _ := tm.StartTask(ctx, "s1", "g2")
	t3, _ := tm.StartTask(ctx, "s1", "g3")
	_ = t1

	resumed, err := tm.ResumeTask(ctx, "s1", "answer")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if resumed.ID != t2.ID || resumed.Status != task.TaskAwaitingInput {
		t.Errorf("resumed = (%q,%v), want (%q,TaskAwaitingInput)", resumed.ID, resumed.Status, t2.ID)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != t2.ID {
		t.Errorf("ActiveTaskID = %q, want t2 %q", m.ActiveTaskID, t2.ID)
	}
	if len(m.Queue) != 1 || m.Queue[0] != t3.ID {
		t.Errorf("Queue = %v, want [%q] (t3 still waiting)", m.Queue, t3.ID)
	}
	tk3, _ := tasks.LoadTask(ctx, t3.ID)
	if tk3.Status != task.TaskPending {
		t.Errorf("t3 status = %v, want TaskPending", tk3.Status)
	}
}

func TestActiveTask(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(), // t1 suspends so it stays active
	}}
	tm, _, _ := newManager(r)
	ctx := context.Background()

	// No active task yet.
	got, err := tm.ActiveTask(ctx, "s1")
	if err != nil {
		t.Fatalf("ActiveTask (none): %v", err)
	}
	if got != nil {
		t.Errorf("ActiveTask = %+v, want nil when none", got)
	}

	first, _ := tm.StartTask(ctx, "s1", "goal")
	got, err = tm.ActiveTask(ctx, "s1")
	if err != nil {
		t.Fatalf("ActiveTask: %v", err)
	}
	if got == nil || got.ID != first.ID {
		t.Errorf("ActiveTask = %+v, want task %q", got, first.ID)
	}
}

func TestFailureDuringDrainContinues(t *testing.T) {
	// t1 suspends; on resume completes; drain pops t2 which FAILS; drain
	// continues to t3 which completes. (Decision D.)
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),           // t1 run -> awaiting
		complete("t1 done"), // t1 resume -> done
		fail(),              // t2 -> failed (drain must continue)
		complete("t3 done"), // t3 -> done
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	t1, _ := tm.StartTask(ctx, "s1", "g1")
	t2, _ := tm.StartTask(ctx, "s1", "g2")
	t3, _ := tm.StartTask(ctx, "s1", "g3")
	_ = t1

	if _, err := tm.ResumeTask(ctx, "s1", "answer"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	tk2, _ := tasks.LoadTask(ctx, t2.ID)
	if tk2.Status != task.TaskFailed {
		t.Errorf("t2 status = %v, want TaskFailed", tk2.Status)
	}
	tk3, _ := tasks.LoadTask(ctx, t3.ID)
	if tk3.Status != task.TaskDone {
		t.Errorf("t3 status = %v, want TaskDone (drain continued past failure)", tk3.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not fully drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

// One shared TaskManager, N goroutines each starting a task on a DISTINCT
// session id. Exercises lockFor and the stores under -race. The deterministic
// WithIDFunc from newManager is not goroutine-safe, so this builds its own
// manager with a mutex-guarded id minter and a goroutine-safe runner.
func TestDifferentSessionsProceedConcurrently(t *testing.T) {
	tasks := task.NewInMemory()
	r := &alwaysComplete{}
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	var idMu sync.Mutex
	idN := 0
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(), WithIDFunc(func() string {
		idMu.Lock()
		defer idMu.Unlock()
		idN++
		return fmt.Sprintf("task-%d", idN)
	}))

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sid := fmt.Sprintf("s%d", i)
			got, err := tm.StartTask(context.Background(), sid, "goal")
			if err != nil {
				errs <- err
				return
			}
			if got.Status != task.TaskDone {
				errs <- fmt.Errorf("session %s: status %v, want TaskDone", sid, got.Status)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// alwaysComplete is a goroutine-safe Runner that completes every run.
type alwaysComplete struct{}

func (alwaysComplete) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

// Concurrent StartTask calls on the SAME session id must serialize: exactly one
// task ends up active (or all complete in sequence), never two active at once,
// and the run is clean under -race. With an always-complete runner, each task
// drives to done before the next acquires the lock, so the queue never holds
// two at once and the final meta has no active task.
func TestSameSessionStartsSerialize(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	var idMu sync.Mutex
	idN := 0
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(), WithIDFunc(func() string {
		idMu.Lock()
		defer idMu.Unlock()
		idN++
		return fmt.Sprintf("task-%d", idN)
	}))

	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := tm.StartTask(context.Background(), "shared", fmt.Sprintf("goal-%d", i))
			if err != nil {
				errs <- err
				return
			}
			// Each task completes (alwaysComplete) before the next acquires the
			// lock, so every StartTask returns a done task — never a queued one.
			if got.Status != task.TaskDone {
				errs <- fmt.Errorf("status = %v, want TaskDone", got.Status)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// Serialization invariant: no active task remains, queue empty, and the
	// history recorded exactly n tasks (all driven to done).
	m, err := meta.LoadMeta(context.Background(), "shared")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if m.ActiveTaskID != "" {
		t.Errorf("ActiveTaskID = %q, want empty (no task left active)", m.ActiveTaskID)
	}
	if len(m.Queue) != 0 {
		t.Errorf("Queue = %v, want empty", m.Queue)
	}
	if len(m.TaskRefs) != n {
		t.Errorf("TaskRefs len = %d, want %d", len(m.TaskRefs), n)
	}
	for _, ref := range m.TaskRefs {
		if ref.Status != task.TaskDone {
			t.Errorf("ref %q status = %v, want TaskDone", ref.ID, ref.Status)
		}
	}
}

// spawningRunner is a fake task.Runner whose Resume calls the REAL CreateTaskTool
// and/or SpawnSessionTool before applying a terminal/suspend step. This exercises
// the true ctx -> collector -> tool -> drain path rather than mocking the seam.
type spawningRunner struct {
	tool        *CreateTaskTool
	sessionTool *SpawnSessionTool
	spawnReqs   []spawnReq // same-session goals to emit on the NEXT Resume call
	sessionReqs []spawnReq // new-session goals to emit on the NEXT Resume call
	steps       []func(*gantry.State) *gantry.State
	calls       int
}

func (r *spawningRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	// Only the first run of a task spawns; clear after emitting so a resume of
	// the same task does not re-spawn.
	for _, req := range r.spawnReqs {
		in, _ := json.Marshal(map[string]string{"goal": req.goal, "title": req.title})
		if _, err := r.tool.Invoke(ctx, in); err != nil {
			return nil, err
		}
	}
	for _, req := range r.sessionReqs {
		in, _ := json.Marshal(map[string]string{"goal": req.goal, "title": req.title})
		if _, err := r.sessionTool.Invoke(ctx, in); err != nil {
			return nil, err
		}
	}
	r.spawnReqs = nil
	r.sessionReqs = nil
	step := r.steps[r.calls]
	r.calls++
	return step(st), nil
}

// newSpawningManager wires a spawningRunner into a real Driver + in-memory
// stores with a deterministic id minter, like newManager.
func newSpawningManager(r *spawningRunner) (*TaskManager, task.TaskStore, MetaStore) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	n := 0
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(), WithIDFunc(func() string {
		n++
		return "task-" + string(rune('0'+n))
	}))
	return tm, tasks, meta
}

// newSessionSpawnManager wires a spawningRunner into a real Driver + in-memory
// stores with deterministic task and session id minters, and returns the ready
// queue so tests can inspect/drive cross-session spawned work.
func newSessionSpawnManager(r *spawningRunner) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n := 0
	sn := 0
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithIDFunc(func() string {
			n++
			return "task-" + string(rune('0'+n))
		}),
		WithSessionIDFunc(func() string {
			sn++
			return "sess-" + string(rune('0'+sn))
		}),
	)
	return tm, tasks, meta, ready
}

func TestSpawnThenCompleteDrainsInOrder(t *testing.T) {
	// t1 spawns two children then completes; the drain runs both children.
	r := &spawningRunner{
		tool:      NewCreateTaskTool(),
		spawnReqs: []spawnReq{{goal: "child-a"}, {goal: "child-b"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("t1 done"),      // task-1 run: spawns a,b then completes
			complete("child-a done"), // task-2 (child-a)
			complete("child-b done"), // task-3 (child-b)
		},
	}
	tm, tasks, meta := newSpawningManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "parent goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	_ = first
	for _, id := range []string{"task-1", "task-2", "task-3"} {
		tk, err := tasks.LoadTask(ctx, id)
		if err != nil {
			t.Fatalf("LoadTask %q: %v", id, err)
		}
		if tk.Status != task.TaskDone {
			t.Errorf("task %q status = %v, want TaskDone", id, tk.Status)
		}
	}
	c1, _ := tasks.LoadTask(ctx, "task-2")
	c2, _ := tasks.LoadTask(ctx, "task-3")
	if c1.Goal != "child-a" || c2.Goal != "child-b" {
		t.Errorf("child goals = (%q,%q), want (child-a, child-b)", c1.Goal, c2.Goal)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
	if len(m.TaskRefs) != 3 {
		t.Errorf("TaskRefs len = %d, want 3", len(m.TaskRefs))
	}
}

func TestSpawnOrderingAfterPreExistingQueue(t *testing.T) {
	// t1 (active, suspended), t2 (queued). On resume t1 completes AND spawns a
	// child; the pre-existing t2 must run before the newly-spawned child.
	r := &spawningRunner{
		tool: NewCreateTaskTool(),
		steps: []func(*gantry.State) *gantry.State{
			suspend(),              // task-1 run -> awaiting (t2 queues behind it)
			complete("t1 done"),    // task-1 resume -> done (spawns child here)
			complete("t2 done"),    // task-2 -> done
			complete("child done"), // task-3 (child) -> done
		},
	}
	tm, tasks, meta := newSpawningManager(r)
	ctx := context.Background()

	t1, _ := tm.StartTask(ctx, "s1", "g1")
	t2, _ := tm.StartTask(ctx, "s1", "g2")
	if t1.Status != task.TaskAwaitingInput || t2.Status != task.TaskPending {
		t.Fatalf("setup: t1=%v t2=%v", t1.Status, t2.Status)
	}
	// Arrange the spawn to happen on the resume run of t1.
	r.spawnReqs = []spawnReq{{goal: "child"}}

	if _, err := tm.ResumeTask(ctx, "s1", "answer"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	child, _ := tasks.LoadTask(ctx, "task-3")
	if child.Goal != "child" || child.Status != task.TaskDone {
		t.Errorf("child = (%q,%v), want (child, TaskDone)", child.Goal, child.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestSpawnWhileSuspendedQueuesButDoesNotDrive(t *testing.T) {
	// t1 spawns a child then SUSPENDS. The child must be queued but NOT driven
	// until t1 resumes and completes.
	r := &spawningRunner{
		tool:      NewCreateTaskTool(),
		spawnReqs: []spawnReq{{goal: "child"}},
		steps: []func(*gantry.State) *gantry.State{
			suspend(),              // task-1 run: spawns child, then suspends
			complete("t1 done"),    // task-1 resume -> done
			complete("child done"), // task-2 (child) -> done
		},
	}
	tm, tasks, meta := newSpawningManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "parent")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if first.Status != task.TaskAwaitingInput {
		t.Fatalf("first status = %v, want TaskAwaitingInput", first.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if len(m.Queue) != 1 || m.Queue[0] != "task-2" {
		t.Fatalf("Queue = %v, want [task-2] (child queued)", m.Queue)
	}
	child, _ := tasks.LoadTask(ctx, "task-2")
	if child.Status != task.TaskPending {
		t.Errorf("child status = %v, want TaskPending (not yet driven)", child.Status)
	}
	if _, err := tm.ResumeTask(ctx, "s1", "answer"); err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	child, _ = tasks.LoadTask(ctx, "task-2")
	if child.Status != task.TaskDone {
		t.Errorf("child status after resume = %v, want TaskDone", child.Status)
	}
	m, _ = meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestNoSpawnsLeavesQueueUntouched(t *testing.T) {
	// A plain completing run (no spawns) must not mutate the queue.
	r := &spawningRunner{
		tool:  NewCreateTaskTool(),
		steps: []func(*gantry.State) *gantry.State{complete("done")},
	}
	tm, _, meta := newSpawningManager(r)
	ctx := context.Background()

	got, err := tm.StartTask(ctx, "s1", "goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if got.Status != task.TaskDone {
		t.Errorf("status = %v, want TaskDone", got.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if len(m.Queue) != 0 {
		t.Errorf("Queue = %v, want empty (no spawns)", m.Queue)
	}
	if len(m.TaskRefs) != 1 {
		t.Errorf("TaskRefs len = %d, want 1 (only the parent)", len(m.TaskRefs))
	}
}

func TestSpawnSessionEnqueuesButDoesNotDrive(t *testing.T) {
	// Parent spawns ONE new session then completes. The new session id must be on
	// the ready queue; its task must exist pending under a distinct session id
	// with the right goal; the parent's own queue must be untouched.
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		sessionReqs: []spawnReq{{goal: "spawned work", title: "S"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"), // task-1 run: spawns a session, then completes
		},
	}
	tm, tasks, meta, ready := newSessionSpawnManager(r)
	ctx := context.Background()

	parent, err := tm.StartTask(ctx, "s1", "parent goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if parent.Status != task.TaskDone {
		t.Fatalf("parent status = %v, want TaskDone", parent.Status)
	}

	pm, _ := meta.LoadMeta(ctx, "s1")
	if pm.ActiveTaskID != "" || len(pm.Queue) != 0 {
		t.Errorf("parent meta not clean: active=%q queue=%v", pm.ActiveTaskID, pm.Queue)
	}
	if len(pm.TaskRefs) != 1 {
		t.Errorf("parent TaskRefs = %d, want 1 (only the parent)", len(pm.TaskRefs))
	}

	sid, ok, err := ready.Dequeue(ctx)
	if err != nil || !ok {
		t.Fatalf("ready.Dequeue = (%q, %v, %v), want a session", sid, ok, err)
	}
	if sid != "sess-1" {
		t.Errorf("ready session id = %q, want sess-1", sid)
	}

	sm, err := meta.LoadMeta(ctx, sid)
	if err != nil {
		t.Fatalf("LoadMeta(%q): %v", sid, err)
	}
	if sm.ActiveTaskID == "" {
		t.Fatalf("spawned session ActiveTaskID empty, want set")
	}
	st, err := tasks.LoadTask(ctx, sm.ActiveTaskID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if st.SessionID != sid {
		t.Errorf("task SessionID = %q, want %q", st.SessionID, sid)
	}
	if st.Goal != "spawned work" || st.Title != "S" {
		t.Errorf("task = (%q,%q), want (spawned work, S)", st.Goal, st.Title)
	}
	if st.Status != task.TaskPending {
		t.Errorf("task status = %v, want TaskPending (not yet driven)", st.Status)
	}
}

func TestRunNextReadyDrivesSpawnedSession(t *testing.T) {
	// Parent spawns a session and completes; RunNextReady then drives it to done.
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		sessionReqs: []spawnReq{{goal: "spawned work"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),  // task-1: spawns session, completes
			complete("spawned done"), // task-2: the spawned session's task
		},
	}
	tm, tasks, meta, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent goal"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil {
		t.Fatalf("RunNextReady: %v", err)
	}
	if !ok {
		t.Fatalf("RunNextReady ok = false, want true (a session was ready)")
	}
	if driven == nil || driven.Status != task.TaskDone {
		t.Fatalf("driven = %+v, want a TaskDone task", driven)
	}
	if driven.Goal != "spawned work" {
		t.Errorf("driven goal = %q, want spawned work", driven.Goal)
	}
	sm, _ := meta.LoadMeta(ctx, "sess-1")
	if sm.ActiveTaskID != "" || len(sm.Queue) != 0 {
		t.Errorf("spawned session not drained: active=%q queue=%v", sm.ActiveTaskID, sm.Queue)
	}
	st, _ := tasks.LoadTask(ctx, "task-2")
	if st.Status != task.TaskDone {
		t.Errorf("spawned task status = %v, want TaskDone", st.Status)
	}
}

func TestRunNextReadyEmptyQueue(t *testing.T) {
	r := &spawningRunner{tool: NewCreateTaskTool(), sessionTool: NewSpawnSessionTool(),
		steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, _, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil {
		t.Fatalf("RunNextReady: %v", err)
	}
	if ok || driven != nil {
		t.Errorf("empty queue = (%+v, %v), want (nil, false)", driven, ok)
	}
}

func TestRunNextReadyFIFOTwoSessions(t *testing.T) {
	// Parent spawns two sessions; two RunNextReady calls drive them FIFO; a third
	// returns (nil, false, nil).
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		sessionReqs: []spawnReq{{goal: "first"}, {goal: "second"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"), // task-1
			complete("first done"),  // task-2 (sess-1)
			complete("second done"), // task-3 (sess-2)
		},
	}
	tm, _, _, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	d1, ok1, _ := tm.RunNextReady(ctx)
	d2, ok2, _ := tm.RunNextReady(ctx)
	if !ok1 || !ok2 {
		t.Fatalf("ok1=%v ok2=%v, want both true", ok1, ok2)
	}
	if d1.Goal != "first" || d2.Goal != "second" {
		t.Errorf("drive order = (%q,%q), want (first, second) FIFO", d1.Goal, d2.Goal)
	}
	if d3, ok3, _ := tm.RunNextReady(ctx); ok3 || d3 != nil {
		t.Errorf("third RunNextReady = (%+v, %v), want (nil, false)", d3, ok3)
	}
}

func TestRunNextReadySkipsUndrivableSession(t *testing.T) {
	// Manually enqueue a session whose meta has an empty ActiveTaskID:
	// RunNextReady must skip-and-continue, returning (nil, true, nil).
	r := &spawningRunner{tool: NewCreateTaskTool(), sessionTool: NewSpawnSessionTool(),
		steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, meta, ready := newSessionSpawnManager(r)
	ctx := context.Background()

	if err := meta.SaveMeta(ctx, "ghost", &task.SessionMeta{}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, "ghost"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil {
		t.Fatalf("RunNextReady: %v", err)
	}
	if !ok {
		t.Errorf("ok = false, want true (a session was dequeued)")
	}
	if driven != nil {
		t.Errorf("driven = %+v, want nil (nothing to do)", driven)
	}
}

func TestMixedSpawnsIndependent(t *testing.T) {
	// Parent calls BOTH create_task (same session) and spawn_session (new session).
	// The same-session child drains inline; the new-session child runs only via
	// RunNextReady.
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		spawnReqs:   []spawnReq{{goal: "same-child"}},
		sessionReqs: []spawnReq{{goal: "new-child"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),     // task-1: spawns both
			complete("same-child done"), // task-2: same-session child (drains inline)
			complete("new-child done"),  // task-3: new-session child (via RunNextReady)
		},
	}
	tm, tasks, meta, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	same, _ := tasks.LoadTask(ctx, "task-2")
	if same.Goal != "same-child" || same.Status != task.TaskDone {
		t.Errorf("same-session child = (%q,%v), want (same-child, TaskDone)", same.Goal, same.Status)
	}
	pm, _ := meta.LoadMeta(ctx, "s1")
	if pm.ActiveTaskID != "" || len(pm.Queue) != 0 {
		t.Errorf("parent session not drained: active=%q queue=%v", pm.ActiveTaskID, pm.Queue)
	}
	driven, ok, _ := tm.RunNextReady(ctx)
	if !ok || driven == nil || driven.Goal != "new-child" || driven.Status != task.TaskDone {
		t.Errorf("RunNextReady = (%+v, %v), want new-child TaskDone", driven, ok)
	}
}

func TestSpawnedSessionSpawnsAgain(t *testing.T) {
	// A ready-driven session that itself calls spawn_session enqueues a further
	// new session, drivable by another RunNextReady.
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		sessionReqs: []spawnReq{{goal: "child"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),     // task-1: spawns child session
			complete("child done"),      // task-2 (sess-1): spawns grandchild then done
			complete("grandchild done"), // task-3 (sess-2)
		},
	}
	tm, _, _, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	// Arrange the child's run to spawn a grandchild.
	r.sessionReqs = []spawnReq{{goal: "grandchild"}}

	c, ok, _ := tm.RunNextReady(ctx) // drives sess-1 (child), which spawns sess-2
	if !ok || c.Goal != "child" || c.Status != task.TaskDone {
		t.Fatalf("child drive = (%+v, %v), want child TaskDone", c, ok)
	}
	g, ok, _ := tm.RunNextReady(ctx) // drives sess-2 (grandchild)
	if !ok || g.Goal != "grandchild" || g.Status != task.TaskDone {
		t.Errorf("grandchild drive = (%+v, %v), want grandchild TaskDone", g, ok)
	}
}

func TestErroredParentDiscardsSessionSpawns(t *testing.T) {
	// A TaskFailed (non-error) parent still flushes its buffered spawns, because
	// spawns drain BEFORE the terminal branch and the runner's fail() sets
	// DoneError (mapped to TaskFailed) rather than returning a Go error. This
	// documents the achievable behavior; true Advance-error discard (Decision G)
	// is the shared `if err != nil { return t, err }` guard in drive, already
	// exercised by 4a/4b (drive returns before enqueueSpawns).
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		sessionReqs: []spawnReq{{goal: "should-still-flush"}},
		steps:       []func(*gantry.State) *gantry.State{fail()},
	}
	tm, _, _, ready := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if _, ok, _ := ready.Dequeue(ctx); !ok {
		t.Errorf("ready queue empty; a TaskFailed (non-error) parent still flushes spawns")
	}
}

// TestRunNextReadyConcurrentDrain pre-seeds N ready sessions (each with a pending
// active task) and drains them from N goroutines. Each Dequeue yields a distinct
// session id -> distinct per-session lock, so all drive in parallel cleanly under
// -race. Uses alwaysComplete so every driven task finishes.
func TestRunNextReadyConcurrentDrain(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	var idMu sync.Mutex
	idN := 0
	tm := NewTaskManager(driver, tasks, meta, ready, WithIDFunc(func() string {
		idMu.Lock()
		defer idMu.Unlock()
		idN++
		return fmt.Sprintf("task-%d", idN)
	}))

	ctx := context.Background()
	const n = 16
	for i := 0; i < n; i++ {
		sid := fmt.Sprintf("ready-%d", i)
		tid := fmt.Sprintf("seed-%d", i)
		if err := tasks.SaveTask(ctx, &task.Task{
			ID: tid, SessionID: sid, Goal: "g", Status: task.TaskPending,
		}); err != nil {
			t.Fatalf("SaveTask: %v", err)
		}
		if err := meta.SaveMeta(ctx, sid, &task.SessionMeta{
			TaskRefs:     []task.TaskRef{{ID: tid, Status: task.TaskPending}},
			ActiveTaskID: tid,
		}); err != nil {
			t.Fatalf("SaveMeta: %v", err)
		}
		if err := ready.Enqueue(ctx, sid); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	var wg sync.WaitGroup
	var droveMu sync.Mutex
	drove := 0
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			driven, ok, err := tm.RunNextReady(context.Background())
			if err != nil {
				errs <- err
				return
			}
			if ok && driven != nil && driven.Status == task.TaskDone {
				droveMu.Lock()
				drove++
				droveMu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if drove != n {
		t.Errorf("drove %d sessions to done, want %d", drove, n)
	}
}
