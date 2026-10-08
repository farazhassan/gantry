package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

type truncateMessages struct{ max int }

// TruncateMessages returns a step that cuts any message whose estimated size
// exceeds maxTokens down to roughly its first and last halves of the allowed
// size, joined by a "[… ~N tokens truncated …]" marker, on UTF-8 boundaries.
// Tool results are truncated anywhere, including the newest turn; user and
// assistant content only outside the newest turn, so the current request is
// never cut. Summaries are never truncated. The threshold is per message, so
// Budget.MaxTokens is ignored. It panics if maxTokens < 1.
func TruncateMessages(maxTokens int) Compactor {
	if maxTokens < 1 {
		panic(fmt.Sprintf("compactor: TruncateMessages requires maxTokens >= 1, got %d", maxTokens))
	}
	return &truncateMessages{max: maxTokens}
}

func (*truncateMessages) Name() string { return "truncate_messages" }

func (t *truncateMessages) Compact(_ context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	out := cloneMessages(msgs)
	_, turns := segment(out)
	newest := len(out)
	if len(turns) > 0 {
		newest = turns[len(turns)-1].start
	}
	for i, m := range out {
		if isSummary(m) || (m.Role != gantry.RoleTool && i >= newest) {
			continue
		}
		n := b.Count(m)
		if n <= t.max || m.Content == "" {
			continue
		}
		// Keep the share of Content the token cap allows.
		keep := len(m.Content) * t.max / n
		if keep >= len(m.Content) {
			continue
		}
		half := keep / 2
		head := m.Content[:runeStartAtOrBefore(m.Content, half)]
		tail := m.Content[runeStartAtOrAfter(m.Content, len(m.Content)-half):]
		m.Content = head + fmt.Sprintf("\n[… ~%d tokens truncated …]\n", n-t.max) + tail
		out[i] = m
	}
	return out, nil
}
