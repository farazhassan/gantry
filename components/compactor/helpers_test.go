package compactor_test

import (
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
)

// lenCount counts one token per content byte, so expectations are exact.
func lenCount(m gantry.Message) int { return len(m.Content) }

var lenBudget = compactor.Budget{Counter: lenCount}

func user(s string) gantry.Message { return gantry.Message{Role: gantry.RoleUser, Content: s} }
func assistant(s string) gantry.Message {
	return gantry.Message{Role: gantry.RoleAssistant, Content: s}
}

func call(id string) gantry.Message {
	return gantry.Message{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{{ID: id, Name: "t"}}}
}

func result(id, content string) gantry.Message {
	return gantry.Message{Role: gantry.RoleTool, ToolCallID: id, Name: "t", Content: content}
}

func xs(n int) string { return strings.Repeat("x", n) }

// userTurns returns n single-message turns of size bytes each.
func userTurns(n, size int) []gantry.Message {
	out := make([]gantry.Message, n)
	for i := range out {
		out[i] = user(xs(size))
	}
	return out
}

const clearedPlaceholder = "[tool result cleared to save context]"
const summaryPrefix = "[Summary of earlier conversation]\n"
