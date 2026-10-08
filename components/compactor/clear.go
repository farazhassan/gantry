package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

// clearedPlaceholder replaces the content of a cleared tool result.
const clearedPlaceholder = "[tool result cleared to save context]"

type clearToolResults struct{ keep int }

// ClearToolResults returns a step that, in turns older than the newest
// keepTurns, replaces each tool result's Content with a short placeholder,
// oldest first, keeping ToolCallID and Name so the call/result pairing stays
// valid. Results already no longer than the placeholder are skipped, and
// tool-call Input is never touched. With Budget.MaxTokens > 0 it stops once
// the messages fit. It panics if keepTurns < 0.
func ClearToolResults(keepTurns int) Compactor {
	if keepTurns < 0 {
		panic(fmt.Sprintf("compactor: ClearToolResults requires keepTurns >= 0, got %d", keepTurns))
	}
	return &clearToolResults{keep: keepTurns}
}

func (*clearToolResults) Name() string { return "clear_tool_results" }

func (c *clearToolResults) Compact(_ context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	out := cloneMessages(msgs)
	_, turns := segment(out)
	total := totalTokens(out, b)
	for _, t := range olderTurns(turns, c.keep) {
		for i := t.start; i < t.end; i++ {
			if b.MaxTokens > 0 && total <= b.MaxTokens {
				return out, nil
			}
			m := out[i]
			if m.Role != gantry.RoleTool || len(m.Content) <= len(clearedPlaceholder) {
				continue
			}
			before := b.Count(m)
			m.Content = clearedPlaceholder
			total += b.Count(m) - before
			out[i] = m
		}
	}
	return out, nil
}
