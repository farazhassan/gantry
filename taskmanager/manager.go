package taskmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/farazhassan/gantry"
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

	spawnErrHandler func(error)

	mu    sync.Mutex
	locks map[string]*sessionLock

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

// WithSpawnErrorHandler sets a callback invoked when a drain-time spawn problem
// is recorded — today, a create_task depends_on referencing an id that is not a
// task in the spawning session (Decision I). The spawn is persisted cancelled
// rather than failing the parent's drive, so this callback is the only signal.
// Default no-op. Doubles as the observability seam (mirrors the Dispatcher's
// WithErrorHandler and the Scheduler's WithScheduleErrorHandler). A nil f is
// ignored (the default is kept).
func WithSpawnErrorHandler(f func(error)) Option {
	return func(m *TaskManager) {
		if f != nil {
			m.spawnErrHandler = f
		}
	}
}

// NewTaskManager builds a TaskManager over a Driver, the same TaskStore the
// Driver persists through, a MetaStore, and a ReadyQueue for cross-session
// spawned work. It panics if any is nil.
func NewTaskManager(driver *task.Driver, tasks task.TaskStore, meta MetaStore, ready ReadyQueue, opts ...Option) *TaskManager {
	if driver == nil || tasks == nil || meta == nil || ready == nil {
		panic("taskmanager: NewTaskManager requires non-nil driver, tasks, meta, and ready")
	}
	m := &TaskManager{
		driver:          driver,
		tasks:           tasks,
		meta:            meta,
		ready:           ready,
		newID:           newTaskID,
		newSessionID:    newSessionID,
		spawnErrHandler: func(error) {},
		locks:           make(map[string]*sessionLock),
		cancels:         make(map[string]context.CancelFunc),
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

// sessionLock pairs the per-session mutex with a reference count guarded by
// TaskManager.mu. The count tracks how many goroutines currently hold or are
// waiting on the mutex; when it drops to zero the entry is evicted, so idle
// sessions do not leak a map entry forever.
type sessionLock struct {
	mu   sync.Mutex
	refs int
}

// acquire returns the session's lock, locked. It mints the entry on first use
// and increments the refcount under m.mu BEFORE blocking on the session mutex,
// so a concurrent release can never evict an entry that still has a holder or
// waiter — no lost wakeup and no duplicate lock for the same session id.
// Different session ids get different locks and never block each other.
func (m *TaskManager) acquire(sessionID string) *sessionLock {
	m.mu.Lock()
	lk, ok := m.locks[sessionID]
	if !ok {
		lk = &sessionLock{}
		m.locks[sessionID] = lk
	}
	lk.refs++
	m.mu.Unlock()
	lk.mu.Lock()
	return lk
}

// release unlocks the session's lock and drops its reference; the last
// reference evicts the map entry. Pass the exact *sessionLock returned by
// acquire.
func (m *TaskManager) release(sessionID string, lk *sessionLock) {
	lk.mu.Unlock()
	m.mu.Lock()
	lk.refs--
	if lk.refs == 0 {
		delete(m.locks, sessionID)
	}
	m.mu.Unlock()
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
	lk := m.acquire(sessionID)
	defer m.release(sessionID, lk)

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
	lk := m.acquire(sessionID)
	defer m.release(sessionID, lk)

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

// ResumeTaskWithAnswers supplies per-call answers to the session's active
// awaiting_input task, drives it onward, and drains the queue if it completes.
// answers is keyed by pending tool-call ID (Task.Pending[i].ID); a pending
// call with a missing or empty answer records the task.NoAnswer placeholder.
// Returns ErrNoTaskAwaitingInput if there is no active task or it is not
// awaiting input. For a rejection-cap park (awaiting input with no pending
// calls) use ResumeTask — the driver rejects per-call answers there and that
// error is returned as-is.
func (m *TaskManager) ResumeTaskWithAnswers(ctx context.Context, sessionID string, answers map[string]string) (*task.Task, error) {
	lk := m.acquire(sessionID)
	defer m.release(sessionID, lk)

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
	return m.driveWith(ctx, sessionID, sm, t, func(c context.Context, tk *task.Task) (*task.Task, error) {
		return m.driver.AdvanceWithAnswers(c, tk, answers)
	})
}

// ActiveTask returns the session's current active task, or (nil, nil) if none.
//
// It deliberately does NOT take the per-session lock, so it never blocks behind
// an in-flight drive — a busy session stays observable. The cost is eventual
// consistency: mid-drive you may observe the pre-drive snapshot (the last
// persisted task state, e.g. still pending/active moments before it suspends or
// completes), and because the meta read and the task read are two separate
// store reads they may straddle a drive's save points (it can even report a
// just-finished task while the drive is popping the queue). Treat the result as
// a point-in-time observation, not a lock-protected truth. Stores must be safe
// for concurrent use — the in-memory implementations are (mutex-guarded,
// deep-copying on save and load); see the MetaStore doc for the requirement.
func (m *TaskManager) ActiveTask(ctx context.Context, sessionID string) (*task.Task, error) {
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
	lk := m.acquire(sessionID)
	defer m.release(sessionID, lk)

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

// RunNextReady dequeues one ready session (spawned cross-session work, or a
// same-session task started via StartTaskAsync) and drives its active task to
// suspension or terminal via the existing drive engine. It returns
// (task, true, nil) for a driven session; (nil, false, nil) when the ready
// queue is empty; (nil, true, nil) when the dequeued session has nothing
// drivable (Decision H): an empty ActiveTaskID, an already-terminal active
// task, or an active task parked awaiting_input — driving that one would feed
// its goal to its pending ask_user call, so the resume is left to ResumeTask.
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

	lk := m.acquire(sid)
	defer m.release(sid, lk)

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
	if t.Status == task.TaskAwaitingInput {
		// Parked for a human answer (StartTaskAsync may have re-enqueued the
		// session while its active task was suspended). Driving here would feed
		// the task's goal to its pending ask_user call as if it were the user's
		// reply. Skip; ResumeTask owns this transition, and the queue behind the
		// parked task drains when that inline resume completes.
		return nil, true, nil
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

// StartTaskAsync creates a task for the session WITHOUT driving it: the task is
// persisted, appended to the session's meta (active if the session has no
// active task, queued behind it otherwise), and the session id is enqueued on
// the ReadyQueue for the Dispatcher (or a manual RunNextReady caller) to drive.
// The returned task is always TaskPending.
//
// Unlike StartTask, the per-session lock is held only for the meta
// read-modify-write — never across a drive. If a drive is in flight for this
// session, StartTaskAsync waits for it to release the lock (a wait bounded by
// that drive), appends, and returns; it never joins or starts a drive itself.
//
// It reuses StartDetachedSession's persist-before-enqueue invariant: task and
// meta are durable before the session id becomes visible on the ReadyQueue, so
// a dequeued id always points at a real, loadable session. A ready entry that
// has nothing drivable by dequeue time — the task already drained through an
// inline drive, or the active task is parked awaiting input — is skipped by
// RunNextReady (Decision H).
func (m *TaskManager) StartTaskAsync(ctx context.Context, sessionID, goal string) (*task.Task, error) {
	t := &task.Task{
		ID:        m.newID(),
		SessionID: sessionID,
		Goal:      goal,
		Status:    task.TaskPending,
		CreatedAt: time.Now().UTC(),
	}
	// Persist the task BEFORE it becomes reachable via meta or the ready queue.
	if err := m.tasks.SaveTask(ctx, t); err != nil {
		return nil, err
	}
	if err := m.appendTaskToMeta(ctx, sessionID, t); err != nil {
		return nil, err
	}
	// Persist-before-enqueue: task + meta are saved above, so the id on the
	// queue always points at a real session (mirrors StartDetachedSession).
	if err := m.ready.Enqueue(ctx, sessionID); err != nil {
		return nil, err
	}
	return t, nil
}

// appendTaskToMeta records t in the session's meta — active if none, queued
// behind the active task otherwise — under the per-session lock. The lock is
// held only for this read-modify-write; the caller must not already hold it.
func (m *TaskManager) appendTaskToMeta(ctx context.Context, sessionID string, t *task.Task) error {
	lk := m.acquire(sessionID)
	defer m.release(sessionID, lk)

	sm, err := m.loadOrFreshMeta(ctx, sessionID)
	if err != nil {
		return err
	}
	sm.TaskRefs = append(sm.TaskRefs, task.TaskRef{
		ID:        t.ID,
		Title:     t.Title,
		Status:    t.Status,
		CreatedAt: t.CreatedAt,
	})
	if sm.ActiveTaskID == "" {
		sm.ActiveTaskID = t.ID
	} else {
		sm.Queue = append(sm.Queue, t.ID)
	}
	return m.meta.SaveMeta(ctx, sessionID, sm)
}

// drive advances the active task from a single input string and, when it
// terminates, drains the pending queue dependency-aware. See driveWith for the
// loop contract; input is the goal seed for a freshly-activated task or the
// user's answer on resume — Driver.Advance distinguishes these.
func (m *TaskManager) drive(ctx context.Context, sessionID string, sm *task.SessionMeta, t *task.Task, input string) (*task.Task, error) {
	return m.driveWith(ctx, sessionID, sm, t, func(c context.Context, tk *task.Task) (*task.Task, error) {
		return m.driver.Advance(c, tk, input)
	})
}

// driveWith advances the active task via first — the seeded initial advance —
// and, when it terminates, drains the pending queue dependency-aware via
// nextEligible: the first queued task whose depends_on are all done becomes
// ActiveTaskID and is driven from its own goal (Decisions J, K). It returns
// when a task suspends (awaiting_input) or nothing is eligible. A queued task
// that fails is recorded and the drain continues to the next (Decision D). sm
// is the already-loaded SessionMeta.
func (m *TaskManager) driveWith(ctx context.Context, sessionID string, sm *task.SessionMeta, t *task.Task, first func(context.Context, *task.Task) (*task.Task, error)) (*task.Task, error) {
	driveCtx, cancelFn := context.WithCancel(ctx)
	m.registerCancel(sessionID, cancelFn)
	defer m.deregisterCancel(sessionID, cancelFn)

	advance := first
	var err error
	for {
		coll := m.newCollector(t)
		runCtx := withCollector(driveCtx, coll)

		t, err = advance(runCtx, t)
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

		// terminal: done/failed/cancelled — drain the queue dependency-aware.
		sm.ActiveTaskID = ""
		var next *task.Task
		next, err = m.nextEligible(ctx, sm)
		if err != nil {
			return t, err
		}
		if next == nil {
			// Queue empty, or every queued task is blocked on a non-terminal
			// dependency (Decision K): persist and stop. Blocked tasks are
			// re-checked by nextEligible on the session's next terminal, so
			// they cannot be orphaned while the session makes progress.
			if err = m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
				return t, err
			}
			return t, nil
		}
		sm.ActiveTaskID = next.ID
		if err = m.meta.SaveMeta(ctx, sessionID, sm); err != nil {
			return t, err
		}
		t = next
		// Every drained task after the first runs from its own goal.
		advance = func(c context.Context, tk *task.Task) (*task.Task, error) {
			return m.driver.Advance(c, tk, tk.Goal)
		}
	}
}

// nextEligible scans sm.Queue in FIFO order for the first task whose
// dependencies are all TaskDone, removes it from the queue, and returns it.
// Dependency statuses are loaded from the TaskStore (authoritative), not from
// the refs. Along the way it cancels any queued task with a failed/cancelled
// dependency (Decision J): the dependent is persisted TaskCancelled with a
// cause note appended to its Working, its ref is synced, and the scan
// continues at the same index — a dependency is always minted (hence queued)
// before its dependents, so a cancellation at position i can cascade only to
// positions after i, and one bounded forward pass settles the whole queue (no
// livelock). Returns (nil, nil) when the queue is empty or every remaining
// task is blocked on a non-terminal dependency; blocked tasks stay queued and
// are re-checked by this same scan on the session's next terminal (Decision K).
func (m *TaskManager) nextEligible(ctx context.Context, sm *task.SessionMeta) (*task.Task, error) {
	i := 0
	for i < len(sm.Queue) {
		qt, err := m.tasks.LoadTask(ctx, sm.Queue[i])
		if err != nil {
			return nil, err
		}
		var badDep *task.Task // first dependency that ended failed/cancelled
		blocked := false
		for _, depID := range qt.DependsOn {
			dep, err := m.tasks.LoadTask(ctx, depID)
			if err != nil {
				return nil, err
			}
			if dep.Status == task.TaskDone {
				continue
			}
			if dep.Status.IsTerminal() { // failed or cancelled
				badDep = dep
				break // a dead dependency decides the task's fate outright
			}
			blocked = true // non-terminal dep; keep scanning for a dead one
		}
		switch {
		case badDep != nil:
			qt.Status = task.TaskCancelled
			qt.Working = append(qt.Working, gantry.Message{
				Role:    gantry.RoleSystem,
				Content: fmt.Sprintf("Task cancelled: dependency %s ended %s.", badDep.ID, badDep.Status),
			})
			qt.UpdatedAt = time.Now().UTC()
			if err := m.tasks.SaveTask(ctx, qt); err != nil {
				return nil, err
			}
			syncRef(sm, qt)
			sm.Queue = append(sm.Queue[:i], sm.Queue[i+1:]...)
			// i not advanced: the next candidate shifted into position i.
		case blocked:
			i++ // leave it queued; a later terminal re-checks it
		default:
			sm.Queue = append(sm.Queue[:i], sm.Queue[i+1:]...)
			return qt, nil
		}
	}
	return nil, nil
}

// enqueueSpawns drains two buffers from the just-finished run of parent,
// consuming the ids the collector minted at Invoke time (the ids the model
// already saw) and stamping parent linkage, depth, and the policy-derived
// budget on every child:
//   - same-session requests (create_task): each is persisted under its
//     pre-minted id. depends_on ids are validated against the session's
//     TaskRefs history (Decision I): a valid spawn is appended pending to
//     sm.Queue; a spawn with an unknown/foreign/self/forward id is persisted
//     TaskCancelled with a cause note, NOT enqueued, and reported via the
//     spawn error handler — the parent's drive is unaffected. Because refs
//     are appended as the loop goes, a spawn may depend on any earlier
//     same-session task, including earlier spawns from this same drain.
//   - new-session requests (spawn_session): each pre-minted session/task id
//     pair is handed to StartDetachedSession (persist-before-enqueue), and the
//     parent's meta records a ChildRef so cancellation can cascade. The
//     parent's sm.Queue is NOT touched by new-session spawns.
//
// Runs under the session lock, on the orchestrator goroutine, after Advance
// returned — never re-entering the driver. A no-op when both buffers are empty.
func (m *TaskManager) enqueueSpawns(ctx context.Context, sessionID string, sm *task.SessionMeta, parent *task.Task, coll *spawnCollector) error {
	known := make(map[string]bool, len(sm.TaskRefs))
	for _, ref := range sm.TaskRefs {
		known[ref.ID] = true
	}
	for _, req := range coll.drain() {
		nt := &task.Task{
			ID:              req.taskID,
			SessionID:       sessionID,
			Title:           req.title,
			Goal:            req.goal,
			DependsOn:       req.dependsOn,
			Status:          task.TaskPending,
			ParentSessionID: sessionID,
			ParentTaskID:    parent.ID,
			Depth:           parent.Depth + 1,
			Budget:          m.childBudget(parent),
			CreatedAt:       time.Now().UTC(),
		}
		for _, dep := range req.dependsOn {
			if !known[dep] {
				nt.Status = task.TaskCancelled
				nt.Working = append(nt.Working, gantry.Message{
					Role:    gantry.RoleSystem,
					Content: fmt.Sprintf("Task cancelled at creation: depends_on references %q, which is not a task in this session.", dep),
				})
				m.spawnErrHandler(fmt.Errorf("taskmanager: spawned task %s cancelled: depends_on references %q, not a task in session %s", nt.ID, dep, sessionID))
				break
			}
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
		known[nt.ID] = true
		if nt.Status == task.TaskPending {
			sm.Queue = append(sm.Queue, nt.ID)
		}
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
