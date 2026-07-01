package task

import "github.com/farazhassan/gantry"

// Result returns the content of the task's final assistant message — its answer —
// or "" if the task has produced none yet. It scans Working backward for the last
// message with Role RoleAssistant; critic feedback (RoleSystem) is naturally
// excluded. Result is status-independent: callers check t.Status /
// t.Status.IsTerminal() separately to decide whether the answer is final.
func Result(t *Task) string {
	for i := len(t.Working) - 1; i >= 0; i-- {
		if t.Working[i].Role == gantry.RoleAssistant {
			return t.Working[i].Content
		}
	}
	return ""
}
