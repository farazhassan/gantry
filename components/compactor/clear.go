package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

// clearedPlaceholder replaces the content of a cleared tool result.
const clearedPlaceholder = "[tool result cleared to save context]"

type clearToolResults struct{ keep int }

// ClearToolResults returns a step that keeps the newest keepResults tool
// results (RoleTool messages, counted across the whole transcript regardless
// of turns, so a long tool loop in the newest turn is shrunk too) and, oldest
// first, replaces each older result's Content with a short placeholder,
// keeping ToolCallID and Name so the call/result pairing stays valid. Results
// already no longer than the placeholder are skipped, and tool-call Input is
// never touched. With Budget.MaxTokens > 0 it stops once the messages fit. It
// panics if keepResults < 0.
func ClearToolResults(keepResults int) Compactor {
	if keepResults < 0 {
		panic(fmt.Sprintf("compactor: ClearToolResults requires keepResults >= 0, got %d", keepResults))
	}
	return &clearToolResults{keep: keepResults}
}

func (*clearToolResults) Name() string { return "clear_tool_results" }

func (c *clearToolResults) Compact(_ context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	out := cloneMessages(msgs)
	results := 0
	for _, m := range out {
		if m.Role == gantry.RoleTool {
			results++
		}
	}
	clearable := results - c.keep
	total := totalTokens(out, b)
	for i := 0; i < len(out) && clearable > 0; i++ {
		m := out[i]
		if m.Role != gantry.RoleTool {
			continue
		}
		clearable--
		if b.MaxTokens > 0 && total <= b.MaxTokens {
			break
		}
		if len(m.Content) <= len(clearedPlaceholder) {
			continue
		}
		before := b.Count(m)
		m.Content = clearedPlaceholder
		total += b.Count(m) - before
		out[i] = m
	}
	return out, nil
}
