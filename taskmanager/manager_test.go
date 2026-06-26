package taskmanager

import (
	"context"
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
