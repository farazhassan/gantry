package taskmanager

import (
	"context"
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
	tm := NewTaskManager(driver, tasks, meta, WithIDFunc(func() string {
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
	tm := NewTaskManager(driver, tasks, meta, WithIDFunc(func() string {
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
