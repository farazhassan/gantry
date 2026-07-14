package gantry

// Canonical State.Meta keys under which the task layer records which task and
// session a run belongs to. They live here (not in package task) because the
// core run loop reads them when stamping event identity, and package gantry
// must never import package task (import cycle). Package task re-declares
// task.MetaTaskID / task.MetaSessionID as aliases of these constants, so every
// existing task-layer caller keeps compiling unchanged. Values are strings.
const (
	MetaTaskID    = "task.id"
	MetaSessionID = "task.session_id"
)
