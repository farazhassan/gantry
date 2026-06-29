// Package taskmanager orchestrates a session's goal-directed work. It owns and
// drives tasks through the task.Driver: one active task per session id, with a
// pending FIFO queue that drains when the active task completes.
//
// The session package remains a pure chat layer; taskmanager shares only the
// session id as a key. Execution is synchronous and serialized per session id —
// parallelism is "N sessions x 1 active task," achieved by callers invoking
// different session ids concurrently.
package taskmanager
