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
	driver       *task.Driver
	tasks        task.TaskStore
	meta         MetaStore
	ready        ReadyQueue
	newID        func() string
	newSessionID func() string
	policy       SpawnPolicy // zero value: default depth cap, inherit-limits budgets

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc // sessionID -> in-flight drive's cancel
}

// Option configures a TaskManager.
type Option func(*TaskManager)

// WithIDFunc overrides the task-id minter (tests use a deterministic one).
func WithIDFunc(f func() string) Option {
	return func(m *TaskManager) { m.newID = f }
}

// WithSessionIDFunc overrides the session-id minter used when spawning new
// sessions (tests use a deterministic one).
func WithSessionIDFunc(f func() string) Option {
	return func(m *TaskManager) { m.newSessionID = f }
}

// NewTaskManager builds a TaskManager over a Driver, the same TaskStore the
// Driver persists through, a MetaStore, and a ReadyQueue for cross-session
// spawned work. It panics if any is nil.
func NewTaskManager(driver *task.Driver, tasks task.TaskStore, meta MetaStore, ready ReadyQueue, opts ...Option) *TaskManager {
	if driver == nil || tasks == nil || meta == nil || ready == nil {
		panic("taskmanager: NewTaskManager requires non-nil driver, tasks, meta, and ready")
	}
	m := &TaskManager{
		driver:       driver,
		tasks:        tasks,
		meta:         meta,
		ready:        ready,
		newID:        newTaskID,
		newSessionID: newSessionID,
		locks:        make(map[string]*sync.Mutex),
		cancels:      make(map[string]context.CancelFunc),
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

// newSessionID mints a random session id for a spawned new session. Falls back
// to a timestamp if the entropy source fails (never expected in practice).
func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("sess-%d", time.Now().UnixNano())
	}
	return "sess-" + hex.EncodeToString(b[:])
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

// registerCancel records the in-flight drive's cancel func for a session. The
// per-session lock guarantees at most one drive per session, so this never
// overwrites a live entry.
func (m *TaskManager) registerCancel(sessionID string, cancel context.CancelFunc) {
	m.cancelMu.Lock()
	m.cancels[sessionID] = cancel
	m.cancelMu.Unlock()
}

// deregisterCancel removes the session's cancel func and cancels the context to
// free its resources. Safe to call once per drive (via defer).
func (m *TaskManager) deregisterCancel(sessionID string, cancel context.CancelFunc) {
	m.cancelMu.Lock()
	delete(m.cancels, sessionID)
	m.cancelMu.Unlock()
	cancel()
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

// ActiveTask returns the session's current active task, or (nil, nil) if none.
func (m *TaskManager) ActiveTask(ctx context.Context, sessionID string) (*task.Task, error) {
	lk := m.lockFor(sessionID)
	lk.Lock()
	defer lk.Unlock()

	sm, err := m.loadOrFreshMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if sm.ActiveTaskID == "" {
		return nil, nil
	}
	return m.tasks.LoadTask(ctx, sm.ActiveTaskID)
}

// CancelSession stops all work for a session and cascades to the detached child
// sessions its tasks spawned: it interrupts the in-flight run (if any), marks
// the active task plus every queued task TaskCancelled, clears the session's
// active/queue state, then recursively cancels every session in ChildRefs. The
// recursion is bounded by the spawn policy's max depth, so a corrupted
// ChildRefs graph cannot recurse unboundedly; revisiting an already-cancelled
// session is an idempotent no-op. It errors only on a store/meta failure.
func (m *TaskManager) CancelSession(ctx context.Context, sessionID string) error {
	return m.cancelTree(ctx, sessionID, m.policy.maxDepth())
}

// cancelTree cancels one session, then recurses into its ChildRefs with one
// less level of remaining depth. The per-session lock is NOT held across the
// recursion (cancelOne acquires and releases it), so a self- or
// ancestor-referencing ChildRef degrades to an idempotent no-op instead of a
// deadlock.
func (m *TaskManager) cancelTree(ctx context.Context, sessionID string, remaining int) error {
	children, err := m.cancelOne(ctx, sessionID)
	if err != nil {
		return err
	}
	if remaining <= 0 {
		return nil
	}
	for _, cr := range children {
		if cr.SessionID == "" || cr.SessionID == sessionID {
			continue
		}
		if err := m.cancelTree(ctx, cr.SessionID, remaining-1); err != nil {
			return err
		}
	}
	return nil
}

// cancelOne is the single-session cancel: interrupt the in-flight run, mark the
// active + queued tasks TaskCancelled under the session lock, clear
// active/queue state, and return the session's ChildRefs (kept in meta as
// history) for the caller to cascade into.
func (m *TaskManager) cancelOne(ctx context.Context, sessionID string) ([]task.ChildRef, error) {
	// (1) Interrupt any in-flight run WITHOUT taking the per-session lock (the
	// in-flight drive holds it). Cancelling the drive's context unblocks Advance,
	// which the Driver maps to TaskCancelled.
	m.cancelMu.Lock()
	if cancel, ok := m.cancels[sessionID]; ok {
		cancel()
	}
	m.cancelMu.Unlock()

	// (2) Finalize under the per-session lock. This blocks until the interrupted
	// drive releases the lock, so we observe the cancelled active task and a
	// stable queue.
	lk := m.lockFor(sessionID)
	lk.Lock()
	defer lk.Unlock()

	sm, err := m.loadOrFreshMeta(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(sm.Queue)+1)
	if sm.ActiveTaskID != "" {
		ids = append(ids, sm.ActiveTaskID)
	}
	ids = append(ids, sm.Queue...)

	for _, id := range ids {
		t, err := m.tasks.LoadTask(ctx, id)
		if errors.Is(err, task.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if t.Status.IsTerminal() {
			continue // already done/failed/cancelled
		}
		t.Status = task.TaskCancelled
		t.UpdatedAt = time.Now().UTC()
		if err := m.tasks.SaveTask(ctx, t); err != nil {
			return nil, err
		}
		syncRef(sm, t)
	}
	sm.ActiveTaskID = ""
	sm.Queue = nil
	if err := m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
		return nil, err
	}
	return sm.ChildRefs, nil
}

// RunNextReady dequeues one ready session (spawned cross-session work) and drives
// its active task to suspension or terminal via the existing drive engine. It
// returns (task, true, nil) for a driven session; (nil, false, nil) when the
// ready queue is empty; (nil, true, nil) when the dequeued session has nothing
// drivable (empty ActiveTaskID or an already-terminal active task — Decision H).
//
// The caller composes this: loop for a sequential drain, or call from N
// goroutines for parallel drive (each dequeue yields a distinct session id ->
// distinct per-session lock, so goroutines never contend).
//
// A returned error means the session id has already been consumed from the queue
// (FIFO, no claim/ack — Decision E) and is not re-enqueued; the underlying task
// stays durable, so retry is the caller's responsibility.
func (m *TaskManager) RunNextReady(ctx context.Context) (*task.Task, bool, error) {
	sid, ok, err := m.ready.Dequeue(ctx)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil // empty queue
	}

	lk := m.lockFor(sid)
	lk.Lock()
	defer lk.Unlock()

	sm, err := m.loadOrFreshMeta(ctx, sid)
	if err != nil {
		return nil, true, err // session was real; couldn't load its meta
	}
	if sm.ActiveTaskID == "" {
		return nil, true, nil // Decision H: nothing to do
	}
	t, err := m.tasks.LoadTask(ctx, sm.ActiveTaskID)
	if err != nil {
		return nil, true, err
	}
	if t.Status.IsTerminal() {
		return nil, true, nil // Decision H: already finished
	}
	driven, err := m.drive(ctx, sid, sm, t, t.Goal)
	return driven, true, err
}

// detachedSpec collects the optional overrides for StartDetachedSession.
type detachedSpec struct {
	sessionID       string
	taskID          string
	parentSessionID string
	parentTaskID    string
	depth           int
	budget          task.TaskBudget
	hasParent       bool
}

// DetachedOption customizes StartDetachedSession.
type DetachedOption func(*detachedSpec)

// DetachedIDs presets the spawned session and task ids instead of minting fresh
// ones (eager minting: a spawn tool already returned these ids to the model).
// Empty strings fall back to fresh minting for that id.
func DetachedIDs(sessionID, taskID string) DetachedOption {
	return func(s *detachedSpec) {
		s.sessionID = sessionID
		s.taskID = taskID
	}
}

// DetachedParent records the spawning parent on the new task: parent linkage,
// the child's spawn-tree depth, and the child's cross-run budget.
func DetachedParent(parentSessionID, parentTaskID string, depth int, budget task.TaskBudget) DetachedOption {
	return func(s *detachedSpec) {
		s.parentSessionID = parentSessionID
		s.parentTaskID = parentTaskID
		s.depth = depth
		s.budget = budget
		s.hasParent = true
	}
}

// StartDetachedSession mints a new session, creates a pending task in it,
// persists both (the task, then the session meta with that task active plus a
// TaskRef), and enqueues the session on the ReadyQueue — WITHOUT driving it. The
// drive is left to the Dispatcher (or a manual RunNextReady caller). It runs no
// goroutine, so the TaskManager stays synchronous. Returns the created pending
// task, which carries its id and the new session id.
//
// Options: DetachedIDs consumes ids pre-minted by a spawn tool; DetachedParent
// stamps parent linkage, depth, and budget on the child. With no options the
// behavior is unchanged from before (fresh ids, no parent, zero budget).
//
// This is the single source of truth for the persist-before-enqueue +
// new-session invariant: a successfully-enqueued id always points at a real,
// drivable session. Both enqueueSpawns (spawn_session tool) and the Scheduler
// call it.
func (m *TaskManager) StartDetachedSession(ctx context.Context, goal, title string, opts ...DetachedOption) (*task.Task, error) {
	var spec detachedSpec
	for _, opt := range opts {
		opt(&spec)
	}
	newSID := spec.sessionID
	if newSID == "" {
		newSID = m.newSessionID()
	}
	newTID := spec.taskID
	if newTID == "" {
		newTID = m.newID()
	}
	nt := &task.Task{
		ID:        newTID,
		SessionID: newSID,
		Title:     title,
		Goal:      goal,
		Status:    task.TaskPending,
		CreatedAt: time.Now().UTC(),
	}
	if spec.hasParent {
		nt.ParentSessionID = spec.parentSessionID
		nt.ParentTaskID = spec.parentTaskID
		nt.Depth = spec.depth
		nt.Budget = spec.budget
	}
	if err := m.tasks.SaveTask(ctx, nt); err != nil {
		return nil, err
	}
	newMeta := &task.SessionMeta{
		TaskRefs: []task.TaskRef{{
			ID:        nt.ID,
			Title:     nt.Title,
			Status:    nt.Status,
			CreatedAt: nt.CreatedAt,
		}},
		ActiveTaskID: nt.ID,
	}
	// Persist task + meta BEFORE the ready enqueue.
	if err := m.meta.SaveMeta(ctx, newSID, newMeta); err != nil {
		return nil, err
	}
	if err := m.ready.Enqueue(ctx, newSID); err != nil {
		return nil, err
	}
	return nt, nil
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
	driveCtx, cancelFn := context.WithCancel(ctx)
	m.registerCancel(sessionID, cancelFn)
	defer m.deregisterCancel(sessionID, cancelFn)

	var err error
	for {
		coll := m.newCollector(t)
		runCtx := withCollector(driveCtx, coll)

		t, err = m.driver.Advance(runCtx, t, input)
		if err != nil {
			return t, err // errored run: spawns discarded
		}
		if t.Status == task.TaskCancelled {
			// Cancelled: discard this run's spawns and do NOT drain the queue.
			// CancelSession owns clearing the queue + meta under the lock.
			return t, nil
		}

		// Drain spawns BEFORE branching, so suspended AND terminal tasks queue
		// their follow-on work.
		if err = m.enqueueSpawns(ctx, sessionID, sm, t, coll); err != nil {
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

// enqueueSpawns drains two buffers from the just-finished run of parent,
// consuming the ids the collector minted at Invoke time (the ids the model
// already saw) and stamping parent linkage, depth, and the policy-derived
// budget on every child:
//   - same-session requests (create_task): tasks are persisted under their
//     pre-minted ids and appended to sm.Queue so they run in the current
//     session's FIFO after the active task terminates.
//   - new-session requests (spawn_session): each pre-minted session/task id
//     pair is handed to StartDetachedSession (persist-before-enqueue), and the
//     parent's meta records a ChildRef so cancellation can cascade. The
//     parent's sm.Queue is NOT touched by new-session spawns.
//
// Runs under the session lock, on the orchestrator goroutine, after Advance
// returned — never re-entering the driver. A no-op when both buffers are empty.
func (m *TaskManager) enqueueSpawns(ctx context.Context, sessionID string, sm *task.SessionMeta, parent *task.Task, coll *spawnCollector) error {
	for _, req := range coll.drain() {
		nt := &task.Task{
			ID:              req.taskID,
			SessionID:       sessionID,
			Title:           req.title,
			Goal:            req.goal,
			Status:          task.TaskPending,
			ParentSessionID: sessionID,
			ParentTaskID:    parent.ID,
			Depth:           parent.Depth + 1,
			Budget:          m.childBudget(parent),
			CreatedAt:       time.Now().UTC(),
		}
		if err := m.tasks.SaveTask(ctx, nt); err != nil {
			return err
		}
		sm.TaskRefs = append(sm.TaskRefs, task.TaskRef{
			ID:        nt.ID,
			Title:     nt.Title,
			Status:    nt.Status,
			CreatedAt: nt.CreatedAt,
		})
		sm.Queue = append(sm.Queue, nt.ID)
	}
	for _, req := range coll.drainSessions() {
		nt, err := m.StartDetachedSession(ctx, req.goal, req.title,
			DetachedIDs(req.sessionID, req.taskID),
			DetachedParent(sessionID, parent.ID, parent.Depth+1, m.childBudget(parent)),
		)
		if err != nil {
			return err
		}
		sm.ChildRefs = append(sm.ChildRefs, task.ChildRef{
			SessionID: nt.SessionID,
			TaskID:    nt.ID,
			Title:     nt.Title,
		})
	}
	return nil
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
