package taskmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/farazhassan/gantry/task"
)

// ErrNoTaskAwaitingInput is returned by ResumeTask when the session has no
// active task, or its active task is not awaiting input.
var ErrNoTaskAwaitingInput = errors.New("taskmanager: no task awaiting input")

// TaskManager orchestrates a session's tasks: it creates them, tracks the one
// active task plus a pending FIFO queue (via MetaStore), and drives them
// through the task.Driver. Operations on the same session id are serialized;
// different session ids proceed concurrently.
type TaskManager struct {
	driver *task.Driver
	tasks  task.TaskStore
	meta   MetaStore
	newID  func() string

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Option configures a TaskManager.
type Option func(*TaskManager)

// WithIDFunc overrides the task-id minter (tests use a deterministic one).
func WithIDFunc(f func() string) Option {
	return func(m *TaskManager) { m.newID = f }
}

// NewTaskManager builds a TaskManager over a Driver, the same TaskStore the
// Driver persists through, and a MetaStore. It panics if any is nil.
func NewTaskManager(driver *task.Driver, tasks task.TaskStore, meta MetaStore, opts ...Option) *TaskManager {
	if driver == nil || tasks == nil || meta == nil {
		panic("taskmanager: NewTaskManager requires non-nil driver, tasks, and meta")
	}
	m := &TaskManager{
		driver: driver,
		tasks:  tasks,
		meta:   meta,
		newID:  newTaskID,
		locks:  make(map[string]*sync.Mutex),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// newTaskID mints a random task id. Falls back to a timestamp if the entropy
// source fails (never expected in practice).
func newTaskID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("task-%d", time.Now().UnixNano())
	}
	return "task-" + hex.EncodeToString(b[:])
}

// lockFor returns a stable per-session mutex, created on first use. Different
// session ids get different mutexes and never block each other.
func (m *TaskManager) lockFor(sessionID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	lk, ok := m.locks[sessionID]
	if !ok {
		lk = &sync.Mutex{}
		m.locks[sessionID] = lk
	}
	return lk
}

// loadOrFreshMeta loads the session's meta, returning a fresh empty one when
// none exists yet.
func (m *TaskManager) loadOrFreshMeta(ctx context.Context, sessionID string) (*task.SessionMeta, error) {
	sm, err := m.meta.LoadMeta(ctx, sessionID)
	if errors.Is(err, ErrMetaNotFound) {
		return &task.SessionMeta{}, nil
	}
	if err != nil {
		return nil, err
	}
	return sm, nil
}

// StartTask creates a task for the session. If no task is active it drives the
// task (and drains the queue); otherwise it enqueues the task pending. The
// returned task's status reflects whether it ran, suspended, or is queued.
func (m *TaskManager) StartTask(ctx context.Context, sessionID, goal string) (*task.Task, error) {
	lk := m.lockFor(sessionID)
	lk.Lock()
	defer lk.Unlock()

	sm, err := m.loadOrFreshMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	t := &task.Task{
		ID:        m.newID(),
		SessionID: sessionID,
		Goal:      goal,
		Status:    task.TaskPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := m.tasks.SaveTask(ctx, t); err != nil {
		return nil, err
	}
	sm.TaskRefs = append(sm.TaskRefs, task.TaskRef{
		ID:        t.ID,
		Title:     t.Title,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
	})

	if sm.ActiveTaskID == "" {
		sm.ActiveTaskID = t.ID
		if err := m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
			return nil, err
		}
		return m.drive(ctx, sessionID, sm, t, goal)
	}

	sm.Queue = append(sm.Queue, t.ID)
	if err := m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
		return nil, err
	}
	return t, nil
}

// ResumeTask supplies input to the session's active awaiting_input task, drives
// it onward, and drains the queue if it completes. Returns ErrNoTaskAwaitingInput
// if there is no active task or it is not awaiting input.
func (m *TaskManager) ResumeTask(ctx context.Context, sessionID, input string) (*task.Task, error) {
	lk := m.lockFor(sessionID)
	lk.Lock()
	defer lk.Unlock()

	sm, err := m.loadOrFreshMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sm.ActiveTaskID == "" {
		return nil, ErrNoTaskAwaitingInput
	}
	t, err := m.tasks.LoadTask(ctx, sm.ActiveTaskID)
	if err != nil {
		return nil, err
	}
	if t.Status != task.TaskAwaitingInput {
		return nil, ErrNoTaskAwaitingInput
	}
	return m.drive(ctx, sessionID, sm, t, input)
}

// drive advances the active task and, when it terminates, drains the pending
// FIFO queue: pop the head into ActiveTaskID, save meta, and drive it from its
// own goal. It returns when a task suspends (awaiting_input) or the queue is
// empty. A queued task that fails is recorded and the drain continues to the
// next (Decision D). sm is the already-loaded SessionMeta.
//
// input is the goal seed only for the first Advance of a freshly-activated task;
// on resume it is the user's answer. Driver.Advance distinguishes these.
func (m *TaskManager) drive(ctx context.Context, sessionID string, sm *task.SessionMeta, t *task.Task, input string) (*task.Task, error) {
	var err error
	for {
		t, err = m.driver.Advance(ctx, t, input)
		if err != nil {
			return t, err
		}
		syncRef(sm, t)

		if t.Status == task.TaskAwaitingInput {
			if err = m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
				return t, err
			}
			return t, nil // suspended — caller resumes later
		}

		// terminal: done/failed/cancelled
		sm.ActiveTaskID = ""
		if len(sm.Queue) == 0 {
			if err = m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
				return t, err
			}
			return t, nil
		}

		next := sm.Queue[0]
		sm.Queue = sm.Queue[1:]
		sm.ActiveTaskID = next
		if err = m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
			return t, err
		}

		var nt *task.Task
		nt, err = m.tasks.LoadTask(ctx, next)
		if err != nil {
			return t, err
		}
		t = nt
		input = nt.Goal // queued task runs from its own goal
	}
}

// syncRef updates the matching TaskRef.Status in sm.TaskRefs so the history
// reflects the task's current (terminal or suspended) state.
func syncRef(sm *task.SessionMeta, t *task.Task) {
	for i := range sm.TaskRefs {
		if sm.TaskRefs[i].ID == t.ID {
			sm.TaskRefs[i].Status = t.Status
			return
		}
	}
}
