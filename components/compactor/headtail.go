package compactor

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry"
)

// HeadTail keeps the first head messages and the last tail messages,
// dropping everything in between.
type HeadTail struct {
	head, tail int
}

// NewHeadTail returns a HeadTail Compactor. It panics unless head >= 0,
// tail >= 0, and head+tail >= 1: negative counts would panic inside Compact,
// and keeping zero messages from both ends is never useful.
func NewHeadTail(head, tail int) *HeadTail {
	if head < 0 || tail < 0 || head+tail < 1 {
		panic(fmt.Sprintf("compactor: NewHeadTail requires head >= 0, tail >= 0, and head+tail >= 1, got head=%d tail=%d", head, tail))
	}
	return &HeadTail{head: head, tail: tail}
}

func (h *HeadTail) Compact(_ context.Context, msgs []gantry.Message, _ Budget) ([]gantry.Message, error) {
	if len(msgs) <= h.head+h.tail {
		out := make([]gantry.Message, len(msgs))
		copy(out, msgs)
		return out, nil
	}
	out := make([]gantry.Message, 0, h.head+h.tail)
	out = append(out, msgs[:h.head]...)
	out = append(out, msgs[len(msgs)-h.tail:]...)
	return out, nil
}
