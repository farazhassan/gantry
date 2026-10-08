package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

type dropTurns struct {
	keep     int
	pinFirst bool
}

// DropTurns returns a step that removes the oldest whole turns, keeping the
// newest keepTurns, until the messages fit Budget.MaxTokens (with MaxTokens
// 0, every older turn is removed). The preamble and summaries are never
// removed; pinFirst also keeps the first turn. No marker is inserted. It
// panics if keepTurns < 0.
func DropTurns(keepTurns int, pinFirst bool) Compactor {
	if keepTurns < 0 {
		panic(fmt.Sprintf("compactor: DropTurns requires keepTurns >= 0, got %d", keepTurns))
	}
	return &dropTurns{keep: keepTurns, pinFirst: pinFirst}
}

func (*dropTurns) Name() string { return "drop_turns" }

func (d *dropTurns) Compact(_ context.Context, msgs []gantry.Message, b Budget) ([]gantry.Message, error) {
	pre, turns := segment(msgs)
	total := totalTokens(msgs, b)
	drop := make([]bool, len(turns))
	for i := range olderTurns(turns, d.keep) {
		if b.MaxTokens > 0 && total <= b.MaxTokens {
			break
		}
		t := turns[i]
		if (d.pinFirst && i == 0) || isSummary(msgs[t.start]) {
			continue
		}
		drop[i] = true
		total -= totalTokens(msgs[t.start:t.end], b)
	}
	out := make([]gantry.Message, 0, len(msgs))
	out = append(out, msgs[pre.start:pre.end]...)
	for i, t := range turns {
		if !drop[i] {
			out = append(out, msgs[t.start:t.end]...)
		}
	}
	return out, nil
}
