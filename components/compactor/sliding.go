package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

// SlidingWindow keeps the last n messages and drops everything older.
type SlidingWindow struct {
	n int
}

// NewSlidingWindow returns a SlidingWindow Compactor that retains the
// last n messages. It panics if n < 1: a window must retain at least one
// message, and a non-positive n would otherwise panic inside Compact.
func NewSlidingWindow(n int) *SlidingWindow {
	if n < 1 {
		panic(fmt.Sprintf("compactor: NewSlidingWindow requires n >= 1, got %d", n))
	}
	return &SlidingWindow{n: n}
}

func (s *SlidingWindow) Compact(_ context.Context, msgs []gantry.Message, _ Budget) ([]gantry.Message, error) {
	start := 0
	if len(msgs) > s.n {
		start = len(msgs) - s.n
	}
	window := msgs[start:]
	out := make([]gantry.Message, len(window))
	copy(out, window)
	return out, nil
}
