package task

import "github.com/farazhassan/gantry"

// Result returns the task's final answer: the content of the last assistant
// message in Working that is not a tool-call request, or "" if there is none
// yet. Assistant tool-call turns (e.g. a parked ask_user) and critic feedback
// (a RoleSystem message) are skipped, so an in-flight tool call is never
// mistaken for the answer. Result is status-independent — the caller decides
// from the task's status whether the answer is final — and is safe on a nil task.
func Result(t *Task) string {
	if t == nil {
		return ""
	}
	for i := len(t.Working) - 1; i >= 0; i-- {
		m := t.Working[i]
		if m.Role == gantry.RoleAssistant && len(m.ToolCalls) == 0 {
			return m.Content
		}
	}
	return ""
}
