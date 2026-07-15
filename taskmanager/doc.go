// Package taskmanager orchestrates a session's goal-directed work. It owns and
// drives tasks through the task.Driver: one active task per session id, with a
// pending queue that drains dependency-aware when the active task completes —
// the first queued task whose depends_on tasks are all done runs next, a task
// whose dependency failed or was cancelled is cancelled with a cause note, and
// a fully-blocked queue persists until a later terminal re-checks it.
// Dependency edges are same-session only.
//
// Cross-session work travels through a claim-based ReadyQueue (Dequeue claims,
// Ack/Nack settles, Nack redelivers at the tail), drained by the Dispatcher.
// After a crash, TaskManager.Recover re-enqueues every session whose active
// task is still drivable without input. Delivery is at-least-once;
// RunNextReady's under-lock status check makes duplicate deliveries no-ops.
//
// The session package remains a pure chat layer; taskmanager shares only the
// session id as a key. Execution is synchronous and serialized per session id —
// parallelism is "N sessions x 1 active task," achieved by callers invoking
// different session ids concurrently.
//
// Two additive escape hatches keep a busy session usable: StartTaskAsync
// persists and enqueues a task without driving it (the Dispatcher or a
// RunNextReady caller drives it later), and ActiveTask reads without the
// per-session lock, so a mid-drive session stays observable at the cost of
// eventual consistency.
package taskmanager
