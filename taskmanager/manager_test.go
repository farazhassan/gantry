package taskmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
// session id. Exercises the per-session lock and the stores under -race. The deterministic
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
// queue so tests can inspect/drive cross-session spawned work. Extra options
// (e.g. WithSpawnPolicy) are appended after the deterministic minters.
func newSessionSpawnManager(r *spawningRunner, opts ...Option) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n := 0
	sn := 0
	all := append([]Option{
		WithIDFunc(func() string {
			n++
			return "task-" + string(rune('0'+n))
		}),
		WithSessionIDFunc(func() string {
			sn++
			return "sess-" + string(rune('0'+sn))
		}),
	}, opts...)
	tm := NewTaskManager(driver, tasks, meta, ready, all...)
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

func TestStartDetachedSessionPersistsAndEnqueues(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithIDFunc(func() string { return "task-x" }),
		WithSessionIDFunc(func() string { return "sess-x" }),
	)
	ctx := context.Background()

	nt, err := tm.StartDetachedSession(ctx, "the goal", "the title")
	if err != nil {
		t.Fatalf("StartDetachedSession: %v", err)
	}
	if nt.ID != "task-x" || nt.SessionID != "sess-x" || nt.Goal != "the goal" || nt.Title != "the title" || nt.Status != task.TaskPending {
		t.Errorf("returned task = %+v, want (task-x, sess-x, the goal, the title, pending)", nt)
	}

	tk, err := tasks.LoadTask(ctx, "task-x")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if tk.SessionID != "sess-x" || tk.Goal != "the goal" || tk.Title != "the title" || tk.Status != task.TaskPending {
		t.Errorf("task = %+v, want session sess-x goal/title set, pending", tk)
	}

	sm, err := meta.LoadMeta(ctx, "sess-x")
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if sm.ActiveTaskID != "task-x" {
		t.Errorf("ActiveTaskID = %q, want task-x", sm.ActiveTaskID)
	}
	if len(sm.TaskRefs) != 1 || sm.TaskRefs[0].ID != "task-x" {
		t.Errorf("TaskRefs = %+v, want one ref to task-x", sm.TaskRefs)
	}

	got, ok, err := ready.Dequeue(ctx)
	if err != nil || !ok || got != "sess-x" {
		t.Errorf("Dequeue = (%q, %v, %v), want (sess-x, true, nil)", got, ok, err)
	}
}

// cancelBlockingRunner signals when Resume starts, then blocks until the context
// is cancelled and returns ctx.Err(). Used to hold a drive in-flight while another
// goroutine cancels the session. Resume is called exactly once per task run in
// these tests (the cancel ends the task), so the single-use started channel is safe.
type cancelBlockingRunner struct{ started chan struct{} }

func (r *cancelBlockingRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	close(r.started)
	<-ctx.Done()
	return st, ctx.Err()
}

func TestCancelSessionInterruptsActiveRun(t *testing.T) {
	tasks := task.NewInMemory()
	r := &cancelBlockingRunner{started: make(chan struct{})}
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(),
		WithIDFunc(func() string { return "task-1" }))
	ctx := context.Background()

	type res struct {
		tk  *task.Task
		err error
	}
	doneCh := make(chan res, 1)
	go func() {
		tk, err := tm.StartTask(ctx, "s1", "goal")
		doneCh <- res{tk, err}
	}()
	<-r.started // the run is now in-flight, blocked in Resume

	if err := tm.CancelSession(context.Background(), "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	got := <-doneCh
	if got.err != nil {
		t.Fatalf("StartTask returned error: %v", got.err)
	}
	if got.tk.Status != task.TaskCancelled {
		t.Errorf("returned status = %v, want TaskCancelled", got.tk.Status)
	}
	tk, _ := tasks.LoadTask(ctx, "task-1")
	if tk.Status != task.TaskCancelled {
		t.Errorf("persisted status = %v, want TaskCancelled", tk.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("meta not cleared: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestCancelSessionCancelsAwaitingAndQueued(t *testing.T) {
	// First task suspends (awaiting_input, no in-flight run); second queues.
	// CancelSession cancels both via the finalize path and clears meta.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{suspend()}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	first, _ := tm.StartTask(ctx, "s1", "g1")
	second, _ := tm.StartTask(ctx, "s1", "g2")
	if first.Status != task.TaskAwaitingInput || second.Status != task.TaskPending {
		t.Fatalf("setup: first=%v second=%v", first.Status, second.Status)
	}

	if err := tm.CancelSession(ctx, "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	f, _ := tasks.LoadTask(ctx, first.ID)
	s, _ := tasks.LoadTask(ctx, second.ID)
	if f.Status != task.TaskCancelled {
		t.Errorf("first = %v, want TaskCancelled", f.Status)
	}
	if s.Status != task.TaskCancelled {
		t.Errorf("second = %v, want TaskCancelled", s.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("meta not cleared: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
	for _, ref := range m.TaskRefs {
		if ref.Status != task.TaskCancelled {
			t.Errorf("ref %q = %v, want TaskCancelled", ref.ID, ref.Status)
		}
	}
}

func TestCancelSessionIdleNoop(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, meta := newManager(r)
	ctx := context.Background()

	if err := tm.CancelSession(ctx, "nope"); err != nil {
		t.Fatalf("CancelSession on idle session: %v", err)
	}
	m, _ := meta.LoadMeta(ctx, "nope")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("idle meta = %+v, want empty", m)
	}
}

func TestCancelSessionRaceWithCompletion(t *testing.T) {
	// Cancel racing a run must never panic or corrupt meta. alwaysComplete ignores
	// ctx, so the task may finish done or be cancelled depending on interleaving;
	// either way, no task is left active. Run under -race.
	for i := 0; i < 50; i++ {
		tasks := task.NewInMemory()
		driver := task.NewDriver(&alwaysComplete{}, tasks)
		meta := NewInMemoryMetaStore()
		tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(),
			WithIDFunc(func() string { return "task-1" }))
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = tm.StartTask(context.Background(), "s1", "g") }()
		go func() { defer wg.Done(); _ = tm.CancelSession(context.Background(), "s1") }()
		wg.Wait()
		m, _ := meta.LoadMeta(context.Background(), "s1")
		if m.ActiveTaskID != "" {
			t.Fatalf("iter %d: active not cleared: %q", i, m.ActiveTaskID)
		}
	}
}

func TestStartDetachedSessionWithPresetIDs(t *testing.T) {
	// DetachedIDs wins over the manager's minters.
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithIDFunc(func() string { return "task-minted" }),
		WithSessionIDFunc(func() string { return "sess-minted" }),
	)
	ctx := context.Background()

	nt, err := tm.StartDetachedSession(ctx, "g", "t", DetachedIDs("sess-pre", "task-pre"))
	if err != nil {
		t.Fatalf("StartDetachedSession: %v", err)
	}
	if nt.ID != "task-pre" || nt.SessionID != "sess-pre" {
		t.Errorf("ids = (%q,%q), want (task-pre, sess-pre)", nt.ID, nt.SessionID)
	}
	if _, err := tasks.LoadTask(ctx, "task-pre"); err != nil {
		t.Errorf("LoadTask(task-pre): %v, want persisted under the preset id", err)
	}
	sm, err := meta.LoadMeta(ctx, "sess-pre")
	if err != nil {
		t.Fatalf("LoadMeta(sess-pre): %v", err)
	}
	if sm.ActiveTaskID != "task-pre" {
		t.Errorf("ActiveTaskID = %q, want task-pre", sm.ActiveTaskID)
	}
	sid, ok, err := ready.Dequeue(ctx)
	if err != nil || !ok || sid != "sess-pre" {
		t.Errorf("Dequeue = (%q,%v,%v), want (sess-pre, true, nil)", sid, ok, err)
	}
}

func TestStartDetachedSessionWithParent(t *testing.T) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(completeOnceRunner{}, tasks)
	tm := NewTaskManager(driver, tasks, NewInMemoryMetaStore(), NewInMemoryReadyQueue(),
		WithIDFunc(func() string { return "task-x" }),
		WithSessionIDFunc(func() string { return "sess-x" }),
	)
	ctx := context.Background()

	nt, err := tm.StartDetachedSession(ctx, "g", "t",
		DetachedParent("sess-p", "task-p", 2, task.TaskBudget{MaxRuns: 4}))
	if err != nil {
		t.Fatalf("StartDetachedSession: %v", err)
	}
	if nt.ParentSessionID != "sess-p" || nt.ParentTaskID != "task-p" || nt.Depth != 2 {
		t.Errorf("linkage = (%q,%q,%d), want (sess-p, task-p, 2)", nt.ParentSessionID, nt.ParentTaskID, nt.Depth)
	}
	if nt.Budget.MaxRuns != 4 {
		t.Errorf("Budget.MaxRuns = %d, want 4", nt.Budget.MaxRuns)
	}
	persisted, err := tasks.LoadTask(ctx, "task-x")
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	if persisted.ParentTaskID != "task-p" || persisted.Depth != 2 || persisted.Budget.MaxRuns != 4 {
		t.Errorf("persisted linkage lost: %+v", persisted)
	}
}

// idReportingRunner invokes the real spawn tools exactly once, captures the ids
// they return, then completes every run. It proves the eagerly-minted ids the
// model sees are the ids the manager persists under.
type idReportingRunner struct {
	tool        *CreateTaskTool
	sessionTool *SpawnSessionTool

	spawned        bool
	taskID         string // from create_task output
	spawnSessionID string // from spawn_session output
	spawnTaskID    string // from spawn_session output
}

func (r *idReportingRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	if !r.spawned {
		r.spawned = true
		out, err := r.tool.Invoke(ctx, json.RawMessage(`{"goal":"same-child","title":"C"}`))
		if err != nil {
			return nil, err
		}
		var created struct {
			Queued bool   `json:"queued"`
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			return nil, err
		}
		r.taskID = created.TaskID

		out, err = r.sessionTool.Invoke(ctx, json.RawMessage(`{"goal":"new-child","title":"S"}`))
		if err != nil {
			return nil, err
		}
		var spawned struct {
			Spawned   bool   `json:"spawned"`
			SessionID string `json:"session_id"`
			TaskID    string `json:"task_id"`
		}
		if err := json.Unmarshal(out, &spawned); err != nil {
			return nil, err
		}
		r.spawnSessionID = spawned.SessionID
		r.spawnTaskID = spawned.TaskID
	}
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestEagerIDsMatchPersistedTasks(t *testing.T) {
	r := &idReportingRunner{tool: NewCreateTaskTool(), sessionTool: NewSpawnSessionTool()}
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n, sn := 0, 0
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithIDFunc(func() string { n++; return fmt.Sprintf("task-%d", n) }),
		WithSessionIDFunc(func() string { sn++; return fmt.Sprintf("sess-%d", sn) }),
	)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if r.taskID == "" || r.spawnSessionID == "" || r.spawnTaskID == "" {
		t.Fatalf("tools returned empty ids: %+v", r)
	}
	// Same-session child persisted (and drained) under the id the tool returned.
	child, err := tasks.LoadTask(ctx, r.taskID)
	if err != nil {
		t.Fatalf("LoadTask(%q): %v", r.taskID, err)
	}
	if child.Goal != "same-child" || child.SessionID != "s1" {
		t.Errorf("child = (%q,%q), want (same-child, s1)", child.Goal, child.SessionID)
	}
	// Detached spawn persisted under the returned ids; its session is on ready.
	det, err := tasks.LoadTask(ctx, r.spawnTaskID)
	if err != nil {
		t.Fatalf("LoadTask(%q): %v", r.spawnTaskID, err)
	}
	if det.SessionID != r.spawnSessionID || det.Goal != "new-child" {
		t.Errorf("detached = (%q,%q), want (%q, new-child)", det.SessionID, det.Goal, r.spawnSessionID)
	}
	sid, ok, err := ready.Dequeue(ctx)
	if err != nil || !ok || sid != r.spawnSessionID {
		t.Errorf("ready.Dequeue = (%q,%v,%v), want (%q,true,nil)", sid, ok, err, r.spawnSessionID)
	}
}

func TestSpawnedChildrenCarryParentLinkage(t *testing.T) {
	// Parent (task-1, depth 0) spawns a same-session child and a detached child.
	// Both must carry ParentSessionID/ParentTaskID/Depth; only the detached child
	// gets a ChildRef on the parent's meta.
	r := &spawningRunner{
		tool:        NewCreateTaskTool(),
		sessionTool: NewSpawnSessionTool(),
		spawnReqs:   []spawnReq{{goal: "same-child"}},
		sessionReqs: []spawnReq{{goal: "new-child", title: "N"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),     // task-1: spawns both
			complete("same-child done"), // task-2: same-session child drains inline
		},
	}
	tm, tasks, meta, _ := newSessionSpawnManager(r)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	parent, _ := tasks.LoadTask(ctx, "task-1")
	if parent.ParentSessionID != "" || parent.ParentTaskID != "" || parent.Depth != 0 {
		t.Errorf("root task linkage = (%q,%q,%d), want all zero", parent.ParentSessionID, parent.ParentTaskID, parent.Depth)
	}
	same, _ := tasks.LoadTask(ctx, "task-2")
	if same.ParentSessionID != "s1" || same.ParentTaskID != "task-1" || same.Depth != 1 {
		t.Errorf("same-session child linkage = (%q,%q,%d), want (s1, task-1, 1)", same.ParentSessionID, same.ParentTaskID, same.Depth)
	}
	det, _ := tasks.LoadTask(ctx, "task-3")
	if det.ParentSessionID != "s1" || det.ParentTaskID != "task-1" || det.Depth != 1 {
		t.Errorf("detached child linkage = (%q,%q,%d), want (s1, task-1, 1)", det.ParentSessionID, det.ParentTaskID, det.Depth)
	}
	pm, _ := meta.LoadMeta(ctx, "s1")
	if len(pm.ChildRefs) != 1 {
		t.Fatalf("ChildRefs = %+v, want exactly one (detached spawns only)", pm.ChildRefs)
	}
	if pm.ChildRefs[0] != (task.ChildRef{SessionID: "sess-1", TaskID: "task-3", Title: "N"}) {
		t.Errorf("ChildRef = %+v, want {sess-1 task-3 N}", pm.ChildRefs[0])
	}
}

func TestSpawnPolicyBudgetAppliedToChildren(t *testing.T) {
	r := &spawningRunner{
		tool:      NewCreateTaskTool(),
		spawnReqs: []spawnReq{{goal: "child"}},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),
			complete("child done"),
		},
	}
	tm, tasks, _, _ := newSessionSpawnManager(r, WithSpawnPolicy(SpawnPolicy{
		Budget: func(parent *task.Task) task.TaskBudget {
			return task.TaskBudget{MaxRuns: 5, MaxTokens: 1234}
		},
	}))
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	child, _ := tasks.LoadTask(ctx, "task-2")
	if child.Budget.MaxRuns != 5 || child.Budget.MaxTokens != 1234 {
		t.Errorf("child budget = %+v, want MaxRuns 5 MaxTokens 1234", child.Budget)
	}
}

// depthProbeRunner: the first Resume (the parent) spawns a detached child; the
// second Resume (that child, driven via RunNextReady) attempts another spawn,
// records the tool error, and completes anyway — proving the depth gate is a
// model-visible tool error, not a run-killer.
type depthProbeRunner struct {
	sessionTool   *SpawnSessionTool
	calls         int
	childSpawnErr error
}

func (r *depthProbeRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	r.calls++
	if r.calls == 1 {
		if _, err := r.sessionTool.Invoke(ctx, json.RawMessage(`{"goal":"child"}`)); err != nil {
			return nil, err
		}
	} else {
		_, r.childSpawnErr = r.sessionTool.Invoke(ctx, json.RawMessage(`{"goal":"grandchild"}`))
	}
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

// TestSessionLocksEvictedWhenIdle pins the leak fix: before eviction, every
// session id ever touched left a mutex in the map forever.
func TestSessionLocksEvictedWhenIdle(t *testing.T) {
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
	ctx := context.Background()

	for i := 0; i < 100; i++ {
		if _, err := tm.StartTask(ctx, fmt.Sprintf("s%d", i), "goal"); err != nil {
			t.Fatalf("StartTask: %v", err)
		}
	}
	// Read-only ops must not leak either.
	if _, err := tm.ActiveTask(ctx, "never-seen"); err != nil {
		t.Fatalf("ActiveTask: %v", err)
	}

	tm.mu.Lock()
	n := len(tm.locks)
	tm.mu.Unlock()
	if n != 0 {
		t.Errorf("locks map holds %d entries after all operations finished, want 0", n)
	}
}

func TestSessionLockEvictionSurvivesSuspendResume(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspend(),
		complete("done"),
	}}
	tm, _, _ := newManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if first.Status != task.TaskAwaitingInput {
		t.Fatalf("status = %v, want TaskAwaitingInput", first.Status)
	}
	// Suspended-idle: no operation in flight, so the entry must be gone.
	tm.mu.Lock()
	n := len(tm.locks)
	tm.mu.Unlock()
	if n != 0 {
		t.Errorf("locks map holds %d entries while session is suspended-idle, want 0", n)
	}
	// Eviction must not break resume: a fresh entry is minted on demand.
	resumed, err := tm.ResumeTask(ctx, "s1", "answer")
	if err != nil {
		t.Fatalf("ResumeTask: %v", err)
	}
	if resumed.Status != task.TaskDone {
		t.Errorf("resumed status = %v, want TaskDone", resumed.Status)
	}
}

// overlapRunner detects two Resume calls in flight at once. With every caller
// on ONE session id, the per-session lock must make overlap impossible — a
// buggy eviction (removing an entry that still has waiters) would hand two
// goroutines different mutexes for the same session and trip this. The sleep
// widens the race window.
type overlapRunner struct {
	inFlight atomic.Int32
	overlaps atomic.Int32
}

func (r *overlapRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	if r.inFlight.Add(1) > 1 {
		r.overlaps.Add(1)
	}
	time.Sleep(time.Millisecond)
	r.inFlight.Add(-1)
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestSpawnDepthCapBlocksGrandchild(t *testing.T) {
	r := &depthProbeRunner{sessionTool: NewSpawnSessionTool()}
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n, sn := 0, 0
	tm := NewTaskManager(driver, tasks, meta, ready,
		WithIDFunc(func() string { n++; return fmt.Sprintf("task-%d", n) }),
		WithSessionIDFunc(func() string { sn++; return fmt.Sprintf("sess-%d", sn) }),
		WithSpawnPolicy(SpawnPolicy{MaxDepth: 1}),
	)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	// Drive the child (depth 1); its grandchild spawn must be rejected.
	child, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok {
		t.Fatalf("RunNextReady = (ok=%v, err=%v), want a driven child", ok, err)
	}
	if child.Depth != 1 {
		t.Errorf("child depth = %d, want 1", child.Depth)
	}
	if child.Status != task.TaskDone {
		t.Errorf("child status = %v, want TaskDone (run continued past the tool error)", child.Status)
	}
	if r.childSpawnErr == nil || !strings.Contains(r.childSpawnErr.Error(), "depth") {
		t.Errorf("grandchild spawn err = %v, want a depth error", r.childSpawnErr)
	}
	// The rejected grandchild was never enqueued.
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("ready queue not empty; grandchild should not have been enqueued")
	}
}

// seedLinkedSession stores one pending task as sid's active task, with an
// optional ChildRef to a child session. Used to build cancel-cascade trees
// without driving runs.
func seedLinkedSession(t *testing.T, ctx context.Context, tasks task.TaskStore, meta MetaStore, sid, tid, childSID, childTID string) {
	t.Helper()
	if err := tasks.SaveTask(ctx, &task.Task{ID: tid, SessionID: sid, Goal: "g", Status: task.TaskPending}); err != nil {
		t.Fatalf("SaveTask(%q): %v", tid, err)
	}
	sm := &task.SessionMeta{
		ActiveTaskID: tid,
		TaskRefs:     []task.TaskRef{{ID: tid, Status: task.TaskPending}},
	}
	if childSID != "" {
		sm.ChildRefs = []task.ChildRef{{SessionID: childSID, TaskID: childTID}}
	}
	if err := meta.SaveMeta(ctx, sid, sm); err != nil {
		t.Fatalf("SaveMeta(%q): %v", sid, err)
	}
}

func TestCancelSessionCascadesToChildSessions(t *testing.T) {
	// s1 -> s2 -> s3 chain; default MaxDepth (3) covers it all.
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue())
	ctx := context.Background()

	seedLinkedSession(t, ctx, tasks, meta, "s1", "t1", "s2", "t2")
	seedLinkedSession(t, ctx, tasks, meta, "s2", "t2", "s3", "t3")
	seedLinkedSession(t, ctx, tasks, meta, "s3", "t3", "", "")

	if err := tm.CancelSession(ctx, "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	for _, id := range []string{"t1", "t2", "t3"} {
		tk, _ := tasks.LoadTask(ctx, id)
		if tk.Status != task.TaskCancelled {
			t.Errorf("task %q = %v, want TaskCancelled", id, tk.Status)
		}
	}
	for _, sid := range []string{"s1", "s2", "s3"} {
		m, _ := meta.LoadMeta(ctx, sid)
		if m.ActiveTaskID != "" || len(m.Queue) != 0 {
			t.Errorf("session %q meta not cleared: active=%q queue=%v", sid, m.ActiveTaskID, m.Queue)
		}
	}
}

func TestCancelSessionCascadeBoundedByMaxDepth(t *testing.T) {
	// MaxDepth 1: cancelling s1 reaches s2 (one level down) but NOT s3.
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue(),
		WithSpawnPolicy(SpawnPolicy{MaxDepth: 1}))
	ctx := context.Background()

	seedLinkedSession(t, ctx, tasks, meta, "s1", "t1", "s2", "t2")
	seedLinkedSession(t, ctx, tasks, meta, "s2", "t2", "s3", "t3")
	seedLinkedSession(t, ctx, tasks, meta, "s3", "t3", "", "")

	if err := tm.CancelSession(ctx, "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	t1, _ := tasks.LoadTask(ctx, "t1")
	t2, _ := tasks.LoadTask(ctx, "t2")
	t3, _ := tasks.LoadTask(ctx, "t3")
	if t1.Status != task.TaskCancelled || t2.Status != task.TaskCancelled {
		t.Errorf("t1/t2 = %v/%v, want both TaskCancelled", t1.Status, t2.Status)
	}
	if t3.Status != task.TaskPending {
		t.Errorf("t3 = %v, want TaskPending (beyond the depth bound)", t3.Status)
	}
}

func TestCancelSessionCascadeSurvivesCycles(t *testing.T) {
	// Corrupted refs: s1 -> s2 -> s1. Must terminate without deadlock, both
	// sessions cancelled (revisits are idempotent no-ops).
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue())
	ctx := context.Background()

	seedLinkedSession(t, ctx, tasks, meta, "s1", "t1", "s2", "t2")
	seedLinkedSession(t, ctx, tasks, meta, "s2", "t2", "s1", "t1")

	if err := tm.CancelSession(ctx, "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	for _, id := range []string{"t1", "t2"} {
		tk, _ := tasks.LoadTask(ctx, id)
		if tk.Status != task.TaskCancelled {
			t.Errorf("task %q = %v, want TaskCancelled", id, tk.Status)
		}
	}
}

func TestCancelSessionCascadeUnknownChildIsNoop(t *testing.T) {
	// A ChildRef to a session with no meta: the cascade skips it gracefully.
	tasks := task.NewInMemory()
	driver := task.NewDriver(&alwaysComplete{}, tasks)
	meta := NewInMemoryMetaStore()
	tm := NewTaskManager(driver, tasks, meta, NewInMemoryReadyQueue())
	ctx := context.Background()

	seedLinkedSession(t, ctx, tasks, meta, "s1", "t1", "ghost", "tg")

	if err := tm.CancelSession(ctx, "s1"); err != nil {
		t.Fatalf("CancelSession: %v", err)
	}
	t1, _ := tasks.LoadTask(ctx, "t1")
	if t1.Status != task.TaskCancelled {
		t.Errorf("t1 = %v, want TaskCancelled", t1.Status)
	}
}

func TestSessionLockNoDuplicateLockUnderChurn(t *testing.T) {
	tasks := task.NewInMemory()
	r := &overlapRunner{}
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

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := tm.StartTask(context.Background(), "shared", "goal"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := r.overlaps.Load(); got != 0 {
		t.Errorf("%d overlapping runs on one session id — mutual exclusion broke (duplicate lock)", got)
	}
	tm.mu.Lock()
	remaining := len(tm.locks)
	tm.mu.Unlock()
	if remaining != 0 {
		t.Errorf("locks map holds %d entries after churn, want 0", remaining)
	}
}

// suspendCalls yields awaiting-input with one parked ask_user call per id.
func suspendCalls(ids ...string) func(*gantry.State) *gantry.State {
	return func(st *gantry.State) *gantry.State {
		st.Done = true
		st.DoneReason = gantry.DoneClientToolCall
		for _, id := range ids {
			st.PendingToolCalls = append(st.PendingToolCalls, gantry.ToolCall{ID: id, Name: "ask_user"})
		}
		return st
	}
}

func TestResumeTaskWithAnswersAnswersPerCall(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendCalls("q1", "q2"), // t1 run -> awaiting with two parked calls
		complete("t1 done"),      // t1 resume -> done
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	first, err := tm.StartTask(ctx, "s1", "goal")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if first.Status != task.TaskAwaitingInput || len(first.Pending) != 2 {
		t.Fatalf("setup: status=%v pending=%+v", first.Status, first.Pending)
	}

	resumed, err := tm.ResumeTaskWithAnswers(ctx, "s1", map[string]string{"q1": "alpha", "q2": "beta"})
	if err != nil {
		t.Fatalf("ResumeTaskWithAnswers: %v", err)
	}
	if resumed.Status != task.TaskDone {
		t.Errorf("status = %v, want TaskDone", resumed.Status)
	}
	tk, err := tasks.LoadTask(ctx, first.ID)
	if err != nil {
		t.Fatalf("LoadTask: %v", err)
	}
	got := map[string]string{}
	for _, m := range tk.Working {
		if m.Role == gantry.RoleTool {
			got[m.ToolCallID] = m.Content
		}
	}
	if got["q1"] != "alpha" || got["q2"] != "beta" {
		t.Errorf("answers = %v, want q1:alpha q2:beta", got)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestResumeTaskWithAnswersDrainsQueueAfterCompletion(t *testing.T) {
	// t1 suspends with a parked call; t2 queues behind it. Per-call resume of
	// t1 must drain t2 exactly like the single-string ResumeTask does.
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		suspendCalls("q1"),  // t1 run -> awaiting
		complete("t1 done"), // t1 resume -> done
		complete("t2 done"), // t2 (drained) -> done
	}}
	tm, tasks, meta := newManager(r)
	ctx := context.Background()

	t1, _ := tm.StartTask(ctx, "s1", "g1")
	t2, _ := tm.StartTask(ctx, "s1", "g2")
	if t1.Status != task.TaskAwaitingInput || t2.Status != task.TaskPending {
		t.Fatalf("setup: t1=%v t2=%v", t1.Status, t2.Status)
	}

	resumed, err := tm.ResumeTaskWithAnswers(ctx, "s1", map[string]string{"q1": "alpha"})
	if err != nil {
		t.Fatalf("ResumeTaskWithAnswers: %v", err)
	}
	if resumed.ID != t2.ID || resumed.Status != task.TaskDone {
		t.Errorf("resumed = (%q,%v), want the drained (%q,TaskDone)", resumed.ID, resumed.Status, t2.ID)
	}
	tk1, _ := tasks.LoadTask(ctx, t1.ID)
	if tk1.Status != task.TaskDone {
		t.Errorf("t1 status = %v, want TaskDone", tk1.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestResumeTaskWithAnswersNothingAwaiting(t *testing.T) {
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{complete("done")}}
	tm, _, _ := newManager(r)
	ctx := context.Background()

	// No task at all.
	if _, err := tm.ResumeTaskWithAnswers(ctx, "s1", map[string]string{"q1": "x"}); !errors.Is(err, ErrNoTaskAwaitingInput) {
		t.Errorf("err = %v, want ErrNoTaskAwaitingInput (no task)", err)
	}
	// Active task that completed (not awaiting).
	tm.StartTask(ctx, "s1", "goal")
	if _, err := tm.ResumeTaskWithAnswers(ctx, "s1", map[string]string{"q1": "x"}); !errors.Is(err, ErrNoTaskAwaitingInput) {
		t.Errorf("err = %v, want ErrNoTaskAwaitingInput (completed)", err)
	}
}

// toolCallingRunner invokes the REAL CreateTaskTool with a fixed raw input on
// the invokeOn-th Resume call (1-based), then applies the scripted step for
// each call. It exercises the true ctx -> collector -> tool -> drain path.
type toolCallingRunner struct {
	tool     *CreateTaskTool
	input    json.RawMessage
	invokeOn int
	steps    []func(*gantry.State) *gantry.State
	calls    int
}

func (r *toolCallingRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	r.calls++
	if r.calls == r.invokeOn {
		if _, err := r.tool.Invoke(ctx, r.input); err != nil {
			return nil, err
		}
	}
	step := r.steps[r.calls-1]
	return step(st), nil
}

// newDepManager wires any runner into a real Driver + in-memory stores with a
// deterministic id minter ("task-1", "task-2", ...; single-threaded tests
// only). When spawnErrs is non-nil, spawn-drain errors are appended to it via
// WithSpawnErrorHandler. Returns the ready queue for seeding/inspection.
func newDepManager(r task.Runner, spawnErrs *[]error) (*TaskManager, task.TaskStore, MetaStore, *InMemoryReadyQueue) {
	tasks := task.NewInMemory()
	driver := task.NewDriver(r, tasks)
	meta := NewInMemoryMetaStore()
	ready := NewInMemoryReadyQueue()
	n := 0
	opts := []Option{WithIDFunc(func() string {
		n++
		return fmt.Sprintf("task-%d", n)
	})}
	if spawnErrs != nil {
		opts = append(opts, WithSpawnErrorHandler(func(err error) { *spawnErrs = append(*spawnErrs, err) }))
	}
	tm := NewTaskManager(driver, tasks, meta, ready, opts...)
	return tm, tasks, meta, ready
}

func TestUnknownDependencyCancelsSpawnAtDrain(t *testing.T) {
	// Decision I: a depends_on id that is not a task in this session mints the
	// spawn CANCELLED (its eagerly-returned id stays resolvable), never queues
	// it, and reports through WithSpawnErrorHandler.
	r := &toolCallingRunner{
		tool:     NewCreateTaskTool(),
		input:    json.RawMessage(`{"goal":"child","depends_on":["task-nope"]}`),
		invokeOn: 1,
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),
			complete("child done (must not run)"), // spare: keeps a wrong drain from panicking
		},
	}
	var spawnErrs []error
	tm, tasks, meta, _ := newDepManager(r, &spawnErrs)
	ctx := context.Background()

	parent, err := tm.StartTask(ctx, "s1", "parent")
	if err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if parent.Status != task.TaskDone {
		t.Fatalf("parent status = %v, want TaskDone", parent.Status)
	}
	if r.calls != 1 {
		t.Errorf("runner calls = %d, want 1 (cancelled spawn never driven)", r.calls)
	}
	child, err := tasks.LoadTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("LoadTask task-2: %v", err)
	}
	if child.Status != task.TaskCancelled {
		t.Errorf("child status = %v, want TaskCancelled", child.Status)
	}
	if len(child.DependsOn) != 1 || child.DependsOn[0] != "task-nope" {
		t.Errorf("child DependsOn = %v, want [task-nope]", child.DependsOn)
	}
	if len(child.Working) == 0 || !strings.Contains(child.Working[len(child.Working)-1].Content, "task-nope") {
		t.Errorf("child Working = %+v, want a cause note naming task-nope", child.Working)
	}
	if len(spawnErrs) != 1 || !strings.Contains(spawnErrs[0].Error(), "task-nope") {
		t.Errorf("spawn errors = %v, want exactly one naming task-nope", spawnErrs)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if len(m.Queue) != 0 {
		t.Errorf("Queue = %v, want empty (cancelled spawn not enqueued)", m.Queue)
	}
	if len(m.TaskRefs) != 2 || m.TaskRefs[1].Status != task.TaskCancelled {
		t.Errorf("TaskRefs = %+v, want parent + cancelled child ref", m.TaskRefs)
	}
}

func TestForeignSessionDependencyCancelsSpawn(t *testing.T) {
	// A dependency must live in the SAME session: an id that exists in the
	// TaskStore but belongs to another session is rejected exactly like an
	// unknown id (sm.TaskRefs membership is the same-session existence check).
	r := &toolCallingRunner{
		tool:     NewCreateTaskTool(),
		input:    json.RawMessage(`{"goal":"child","depends_on":["task-1"]}`), // task-1 lives in s2
		invokeOn: 2,
		steps: []func(*gantry.State) *gantry.State{
			complete("s2 done"),                   // task-1 in session s2
			complete("parent done"),               // task-2 in session s1 (spawns task-3)
			complete("child done (must not run)"), // spare
		},
	}
	var spawnErrs []error
	tm, tasks, _, _ := newDepManager(r, &spawnErrs)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s2", "other-session work"); err != nil {
		t.Fatalf("StartTask s2: %v", err)
	}
	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask s1: %v", err)
	}
	if r.calls != 2 {
		t.Errorf("runner calls = %d, want 2 (foreign-dep spawn never driven)", r.calls)
	}
	child, err := tasks.LoadTask(ctx, "task-3")
	if err != nil {
		t.Fatalf("LoadTask task-3: %v", err)
	}
	if child.Status != task.TaskCancelled {
		t.Errorf("child status = %v, want TaskCancelled (foreign-session dependency)", child.Status)
	}
	if len(spawnErrs) != 1 {
		t.Errorf("spawn errors = %v, want exactly one", spawnErrs)
	}
}

func TestValidDependsOnPersistsQueuesAndRuns(t *testing.T) {
	// depends_on may reference the spawning (parent) task itself: task-1 is in
	// TaskRefs from StartTask. The child queues pending with DependsOn
	// persisted, and (the parent being done by drain time) runs to done.
	r := &toolCallingRunner{
		tool:     NewCreateTaskTool(),
		input:    json.RawMessage(`{"goal":"child","depends_on":["task-1"]}`),
		invokeOn: 1,
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),
			complete("child done"),
		},
	}
	var spawnErrs []error
	tm, tasks, meta, _ := newDepManager(r, &spawnErrs)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	child, err := tasks.LoadTask(ctx, "task-2")
	if err != nil {
		t.Fatalf("LoadTask task-2: %v", err)
	}
	if child.Status != task.TaskDone {
		t.Errorf("child status = %v, want TaskDone", child.Status)
	}
	if len(child.DependsOn) != 1 || child.DependsOn[0] != "task-1" {
		t.Errorf("child DependsOn = %v, want [task-1] persisted through the drain", child.DependsOn)
	}
	if len(spawnErrs) != 0 {
		t.Errorf("spawn errors = %v, want none", spawnErrs)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

// depSpawningRunner drives the REAL CreateTaskTool with depends_on wiring: on
// its FIRST Resume it creates one task per chain entry, feeding each returned
// task_id into the next entry's depends_on (a linear backward DAG). Later
// Resumes just apply steps. ids records the minted ids in chain order.
type depSpawningRunner struct {
	tool  *CreateTaskTool
	chain []string // goals; entry i>0 depends on the task minted for entry i-1
	ids   []string
	steps []func(*gantry.State) *gantry.State
	calls int
}

func (r *depSpawningRunner) Resume(ctx context.Context, st *gantry.State) (*gantry.State, error) {
	for i, goal := range r.chain {
		req := map[string]any{"goal": goal}
		if i > 0 {
			req["depends_on"] = []string{r.ids[i-1]}
		}
		in, err := json.Marshal(req)
		if err != nil {
			return nil, err
		}
		out, err := r.tool.Invoke(ctx, in)
		if err != nil {
			return nil, err
		}
		var res struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(out, &res); err != nil {
			return nil, err
		}
		r.ids = append(r.ids, res.TaskID)
	}
	r.chain = nil
	step := r.steps[r.calls]
	r.calls++
	return step(st), nil
}

func TestCreateTaskDependencyChainRunsInOrder(t *testing.T) {
	// Parent (task-1) creates A (task-2), B (task-3, deps A), C (task-4, deps
	// B) via the real tool, wiring each returned task_id into the next request.
	// The drain runs A, B, C to done with DependsOn persisted. NOTE: a linear
	// backward chain is already satisfied by FIFO order (deps are minted before
	// dependents), so this test guards the happy path; the red-first behavior
	// for this task lives in the two tests below.
	r := &depSpawningRunner{
		tool:  NewCreateTaskTool(),
		chain: []string{"A", "B", "C"},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),
			complete("A done"),
			complete("B done"),
			complete("C done"),
		},
	}
	tm, tasks, meta, _ := newDepManager(r, nil)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if len(r.ids) != 3 {
		t.Fatalf("minted ids = %v, want 3", r.ids)
	}
	wantDeps := map[string][]string{
		r.ids[0]: nil,
		r.ids[1]: {r.ids[0]},
		r.ids[2]: {r.ids[1]},
	}
	for id, want := range wantDeps {
		tk, err := tasks.LoadTask(ctx, id)
		if err != nil {
			t.Fatalf("LoadTask %q: %v", id, err)
		}
		if tk.Status != task.TaskDone {
			t.Errorf("task %q status = %v, want TaskDone", id, tk.Status)
		}
		if len(tk.DependsOn) != len(want) {
			t.Errorf("task %q DependsOn = %v, want %v", id, tk.DependsOn, want)
			continue
		}
		for i := range want {
			if tk.DependsOn[i] != want[i] {
				t.Errorf("task %q DependsOn = %v, want %v", id, tk.DependsOn, want)
			}
		}
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestFailedDependencyCancelsDependent(t *testing.T) {
	// Decision J: parent creates A and B (deps A). A FAILS when driven; B must
	// be cancelled with a cause note, never driven, and the drain still
	// finishes cleanly.
	r := &depSpawningRunner{
		tool:  NewCreateTaskTool(),
		chain: []string{"A", "B"},
		steps: []func(*gantry.State) *gantry.State{
			complete("parent done"),
			fail(),                            // A -> TaskFailed
			complete("B done (must not run)"), // spare: keeps a wrong drain from panicking
		},
	}
	tm, tasks, meta, _ := newDepManager(r, nil)
	ctx := context.Background()

	if _, err := tm.StartTask(ctx, "s1", "parent"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	if r.calls != 2 {
		t.Errorf("runner calls = %d, want 2 (B was never driven)", r.calls)
	}
	a, err := tasks.LoadTask(ctx, r.ids[0])
	if err != nil {
		t.Fatalf("LoadTask A: %v", err)
	}
	if a.Status != task.TaskFailed {
		t.Fatalf("A status = %v, want TaskFailed", a.Status)
	}
	b, err := tasks.LoadTask(ctx, r.ids[1])
	if err != nil {
		t.Fatalf("LoadTask B: %v", err)
	}
	if b.Status != task.TaskCancelled {
		t.Errorf("B status = %v, want TaskCancelled (failed dependency)", b.Status)
	}
	if len(b.Working) == 0 {
		t.Fatalf("B.Working empty, want a cause note")
	}
	note := b.Working[len(b.Working)-1].Content
	if !strings.Contains(note, r.ids[0]) || !strings.Contains(note, "failed") {
		t.Errorf("cause note = %q, want mention of %q and \"failed\"", note, r.ids[0])
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
	for _, ref := range m.TaskRefs {
		if ref.ID == b.ID && ref.Status != task.TaskCancelled {
			t.Errorf("B ref status = %v, want TaskCancelled (syncRef)", ref.Status)
		}
	}
}

func TestDependencyGateSkipsBlockedTaskAndLaterUnblocks(t *testing.T) {
	// Decision K, proven with a seeded state the live engine cannot produce
	// today (standing in for a durable backend / future shapes): queue head
	// blocked-1 depends on dep-1, which is neither done nor terminal, while
	// free-1 behind it has no deps. The drain must SKIP blocked-1, run free-1,
	// then STOP with blocked-1 still queued and pending — this test returning
	// at all proves the scan terminates (no livelock). Marking dep-1 done and
	// driving any later terminal through the session must then unblock
	// blocked-1 (no orphan).
	r := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		complete("runner done"),  // runner-1 via RunNextReady
		complete("free done"),    // free-1 (skipped past blocked-1)
		complete("second done"),  // the later StartTask, after dep-1 is done
		complete("blocked done"), // blocked-1, finally eligible
	}}
	tm, tasks, meta, ready := newDepManager(r, nil)
	ctx := context.Background()

	now := time.Now().UTC()
	seedTasks := []*task.Task{
		{ID: "dep-1", SessionID: "s1", Goal: "dep", Status: task.TaskActive, CreatedAt: now},
		{ID: "runner-1", SessionID: "s1", Goal: "run me", Status: task.TaskPending, CreatedAt: now},
		{ID: "blocked-1", SessionID: "s1", Goal: "blocked", DependsOn: []string{"dep-1"}, Status: task.TaskPending, CreatedAt: now},
		{ID: "free-1", SessionID: "s1", Goal: "free", Status: task.TaskPending, CreatedAt: now},
	}
	for _, tk := range seedTasks {
		if err := tasks.SaveTask(ctx, tk); err != nil {
			t.Fatalf("SaveTask %q: %v", tk.ID, err)
		}
	}
	sm := &task.SessionMeta{
		TaskRefs: []task.TaskRef{
			{ID: "dep-1", Status: task.TaskActive},
			{ID: "runner-1", Status: task.TaskPending},
			{ID: "blocked-1", Status: task.TaskPending},
			{ID: "free-1", Status: task.TaskPending},
		},
		ActiveTaskID: "runner-1",
		Queue:        []string{"blocked-1", "free-1"},
	}
	if err := meta.SaveMeta(ctx, "s1", sm); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, "s1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Phase 1: drive runner-1; the drain must skip blocked-1 and run free-1.
	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok {
		t.Fatalf("RunNextReady = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if driven.ID != "free-1" || driven.Status != task.TaskDone {
		t.Fatalf("last driven = (%q, %v), want (free-1, TaskDone) — drain must skip the blocked head", driven.ID, driven.Status)
	}
	blocked, _ := tasks.LoadTask(ctx, "blocked-1")
	if blocked.Status != task.TaskPending {
		t.Fatalf("blocked-1 status = %v, want TaskPending (still blocked)", blocked.Status)
	}
	m, _ := meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 1 || m.Queue[0] != "blocked-1" {
		t.Fatalf("persisted blocked state wrong: active=%q queue=%v, want active empty, queue [blocked-1]", m.ActiveTaskID, m.Queue)
	}

	// Phase 2: finish the dependency; any later terminal in the session
	// re-checks the queue and unblocks blocked-1.
	dep, _ := tasks.LoadTask(ctx, "dep-1")
	dep.Status = task.TaskDone
	if err := tasks.SaveTask(ctx, dep); err != nil {
		t.Fatalf("SaveTask dep-1: %v", err)
	}
	if _, err := tm.StartTask(ctx, "s1", "second"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}
	blocked, _ = tasks.LoadTask(ctx, "blocked-1")
	if blocked.Status != task.TaskDone {
		t.Errorf("blocked-1 status = %v, want TaskDone (unblocked by the later terminal)", blocked.Status)
	}
	m, _ = meta.LoadMeta(ctx, "s1")
	if m.ActiveTaskID != "" || len(m.Queue) != 0 {
		t.Errorf("not drained after unblock: active=%q queue=%v", m.ActiveTaskID, m.Queue)
	}
}

func TestRunNextReadyNacksOnDriveErrorThenSettles(t *testing.T) {
	// First delivery: the drive errors (runner returns a Go error), so the
	// claim is NACKED and the session redelivered. The errored run persisted
	// the task as TaskFailed, so the SECOND delivery finds a terminal active
	// task, no-ops (Decision H), and ACKS — the queue ends empty.
	tm, tasks, meta, ready := newDepManager(&errThenCompleteRunner{}, nil)
	ctx := context.Background()
	seedReadySession(t, ctx, tasks, meta, ready, "task-x", "s1", "will error")

	if _, ok, err := tm.RunNextReady(ctx); !ok || err == nil {
		t.Fatalf("first RunNextReady = (_, %v, %v), want ok=true with a drive error", ok, err)
	}
	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || driven != nil {
		t.Fatalf("second RunNextReady = (%+v, %v, %v), want (nil, true, nil) no-op on redelivery", driven, ok, err)
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("queue not empty after settle; the no-op delivery must ACK")
	}
	tk, _ := tasks.LoadTask(ctx, "task-x")
	if tk.Status != task.TaskFailed {
		t.Errorf("task status = %v, want TaskFailed (persisted by the errored run)", tk.Status)
	}
}

func TestRunNextReadyAcksSkippedAwaitingInputSession(t *testing.T) {
	// A parked (awaiting_input) active task must NOT be re-driven by a queue
	// delivery — resuming it with its goal as the "answer" would corrupt the
	// transcript (behavior also pinned by async_test.go's
	// TestRunNextReadySkipsAwaitingInputSession). This variant seeds the parked
	// state directly and additionally asserts the skip ACKS its claim, which is
	// what makes at-least-once delivery a true no-op for parked sessions
	// (relied on by Recover).
	tm, tasks, meta, ready := newDepManager(&alwaysComplete{}, nil)
	ctx := context.Background()
	parked := &task.Task{
		ID: "task-p", SessionID: "s1", Goal: "g", Status: task.TaskAwaitingInput,
		Pending: []gantry.ToolCall{{ID: "call-1", Name: "ask_user"}},
	}
	if err := tasks.SaveTask(ctx, parked); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	if err := meta.SaveMeta(ctx, "s1", &task.SessionMeta{
		ActiveTaskID: "task-p",
		TaskRefs:     []task.TaskRef{{ID: "task-p", Status: task.TaskAwaitingInput}},
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}
	if err := ready.Enqueue(ctx, "s1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || driven != nil {
		t.Fatalf("RunNextReady = (%+v, %v, %v), want (nil, true, nil) skip", driven, ok, err)
	}
	tk, _ := tasks.LoadTask(ctx, "task-p")
	if tk.Status != task.TaskAwaitingInput || len(tk.Working) != 0 {
		t.Errorf("parked task disturbed: status=%v, working=%d msgs; want awaiting_input, untouched", tk.Status, len(tk.Working))
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("queue not empty; the skip must ACK, not redeliver")
	}
}

// countingCompleteRunner completes every run and counts Resume calls.
type countingCompleteRunner struct{ calls int }

func (r *countingCompleteRunner) Resume(_ context.Context, st *gantry.State) (*gantry.State, error) {
	r.calls++
	st.Messages = append(st.Messages, gantry.Message{Role: gantry.RoleAssistant, Content: "done"})
	st.Done = true
	st.DoneReason = gantry.DoneNoToolCalls
	return st, nil
}

func TestRecoverReenqueuesOnlyDrivableSessions(t *testing.T) {
	tm, tasks, meta, ready := newDepManager(&alwaysComplete{}, nil)
	ctx := context.Background()
	seed := func(sid, tid string, status task.TaskStatus) {
		t.Helper()
		if err := tasks.SaveTask(ctx, &task.Task{ID: tid, SessionID: sid, Goal: "g", Status: status}); err != nil {
			t.Fatalf("SaveTask %q: %v", tid, err)
		}
		if err := meta.SaveMeta(ctx, sid, &task.SessionMeta{
			ActiveTaskID: tid,
			TaskRefs:     []task.TaskRef{{ID: tid, Status: status}},
		}); err != nil {
			t.Fatalf("SaveMeta %q: %v", sid, err)
		}
	}
	seed("s-active", "t-a", task.TaskActive)       // crashed mid-run -> recovered
	seed("s-await", "t-w", task.TaskAwaitingInput) // parked for a human -> skipped
	seed("s-done", "t-d", task.TaskDone)           // crash between task save and meta clear -> skipped
	seed("s-pending", "t-p", task.TaskPending)     // never started -> recovered
	if err := meta.SaveMeta(ctx, "s-idle", &task.SessionMeta{}); err != nil { // no active task -> skipped
		t.Fatalf("SaveMeta s-idle: %v", err)
	}
	if err := meta.SaveMeta(ctx, "s-ghost", &task.SessionMeta{ActiveTaskID: "t-missing"}); err != nil { // dangling id -> skipped
		t.Fatalf("SaveMeta s-ghost: %v", err)
	}

	n, err := tm.Recover(ctx)
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if n != 2 {
		t.Errorf("Recover = %d, want 2 (s-active and s-pending only)", n)
	}
	// ListSessions is sorted, so the enqueue order is deterministic.
	first, ok1, _ := ready.Dequeue(ctx)
	second, ok2, _ := ready.Dequeue(ctx)
	if !ok1 || !ok2 || first != "s-active" || second != "s-pending" {
		t.Errorf("recovered = (%q, %q), want (s-active, s-pending)", first, second)
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("extra session enqueued; awaiting/terminal/idle/ghost must be skipped")
	}
}

func TestRecoverThenRunNextReadyDrivesRecoveredWork(t *testing.T) {
	// Simulates restart-after-crash: the durable stores hold a session whose
	// active task is still pending, but the in-memory ready queue (and its
	// claimed set) is empty. Recover re-enqueues it; RunNextReady drives it.
	tm, tasks, meta, _ := newDepManager(&alwaysComplete{}, nil)
	ctx := context.Background()
	if err := tasks.SaveTask(ctx, &task.Task{ID: "task-c", SessionID: "s1", Goal: "crashed work", Status: task.TaskPending}); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	if err := meta.SaveMeta(ctx, "s1", &task.SessionMeta{
		ActiveTaskID: "task-c",
		TaskRefs:     []task.TaskRef{{ID: "task-c", Status: task.TaskPending}},
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	n, err := tm.Recover(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Recover = (%d, %v), want (1, nil)", n, err)
	}
	driven, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || driven == nil || driven.Status != task.TaskDone {
		t.Fatalf("RunNextReady = (%+v, %v, %v), want the recovered task driven to done", driven, ok, err)
	}
}

func TestRecoverDoubleDeliveryIsNoOp(t *testing.T) {
	// Recover is at-least-once: calling it twice double-enqueues the session.
	// The under-lock status check in RunNextReady makes the duplicate delivery
	// a no-op — the task runs exactly once.
	r := &countingCompleteRunner{}
	tm, tasks, meta, ready := newDepManager(r, nil)
	ctx := context.Background()
	if err := tasks.SaveTask(ctx, &task.Task{ID: "task-c", SessionID: "s1", Goal: "g", Status: task.TaskPending}); err != nil {
		t.Fatalf("SaveTask: %v", err)
	}
	if err := meta.SaveMeta(ctx, "s1", &task.SessionMeta{
		ActiveTaskID: "task-c",
		TaskRefs:     []task.TaskRef{{ID: "task-c", Status: task.TaskPending}},
	}); err != nil {
		t.Fatalf("SaveMeta: %v", err)
	}

	if _, err := tm.Recover(ctx); err != nil {
		t.Fatalf("Recover #1: %v", err)
	}
	if _, err := tm.Recover(ctx); err != nil {
		t.Fatalf("Recover #2: %v", err)
	}

	first, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || first == nil || first.Status != task.TaskDone {
		t.Fatalf("first delivery = (%+v, %v, %v), want driven to done", first, ok, err)
	}
	second, ok, err := tm.RunNextReady(ctx)
	if err != nil || !ok || second != nil {
		t.Fatalf("duplicate delivery = (%+v, %v, %v), want (nil, true, nil) no-op", second, ok, err)
	}
	if _, ok, _ := ready.Dequeue(ctx); ok {
		t.Errorf("queue not empty after both deliveries settled")
	}
	if r.calls != 1 {
		t.Errorf("runner calls = %d, want exactly 1 (the duplicate must not re-drive)", r.calls)
	}
}
