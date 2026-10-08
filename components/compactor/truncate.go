package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

type truncateMessages struct{ max int }

// TruncateMessages returns a step that cuts the Content of any message whose
// estimated size exceeds maxTokens, keeping its head and tail joined by a
// "[… ~N tokens truncated …]" marker, on UTF-8 boundaries, so it ends up near
// maxTokens. Only Content is cut (tool-call Input never is), and a message the
// cut would not shrink is left unchanged. Tool results are truncated anywhere,
// including the newest turn; user and assistant content only outside the
// newest turn, so the current request is never cut. Summaries are never
// truncated. The threshold is per message, so Budget.MaxTokens is ignored. It
// panics if maxTokens < 1.
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
		if cut, ok := t.cut(m, b); ok {
			out[i] = cut
		}
	}
	return out, nil
}

// cut returns m with its Content shortened so m is about t.max tokens, or
// false when m is within the cap or cutting Content would not shrink it.
func (t *truncateMessages) cut(m gantry.Message, b Budget) (gantry.Message, bool) {
	n := b.Count(m)
	if n <= t.max || m.Content == "" {
		return m, false
	}
	empty := m
	empty.Content = ""
	contentTokens := n - b.Count(empty)
	if contentTokens <= 0 {
		return m, false
	}
	size := len(m.Content)
	excess := n - t.max
	remove := min(size, (size*excess+contentTokens-1)/contentTokens)
	if probe := len(truncationMarker(excess)); remove <= probe {
		// A cut smaller than the marker would grow the message: also remove
		// the marker's size.
		remove = min(size, remove+probe)
	}
	keep := size - remove
	head := m.Content[:runeStartAtOrBefore(m.Content, keep/2)]
	tail := m.Content[runeStartAtOrAfter(m.Content, size-(keep-keep/2)):]
	removed := size - len(head) - len(tail)
	cut := m
	cut.Content = head + truncationMarker(contentTokens*removed/size) + tail
	if b.Count(cut) >= n {
		return m, false
	}
	return cut, true
}

func truncationMarker(tokens int) string {
	return fmt.Sprintf("\n[… ~%d tokens truncated …]\n", tokens)
}
