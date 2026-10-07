// Package compactor defines the Compactor interface and reference
// implementations for trimming/summarizing conversation history before
// the LLM call.
//
// The New middleware also recovers from context overflow: when the LLM call
// fails with gantry.ErrContextLengthExceeded it compacts once with
// Budget.Force and retries the call exactly once.
package compactor

import (
	"context"

	"github.com/farazhassan/gantry"
)

// Compactor reduces a message slice to fit within Budget.
//
// Compact must return a slice that does not alias the input's backing array:
// the caller may retain, reslice, reorder, or replace whole elements of the
// result without affecting msgs, and vice versa. Implementations that "pass
// through" unchanged input must still return such an independent copy.
//
// Independence is at the slice level only. The returned Message values are
// shallow copies; reference-typed fields — notably ToolCalls and each
// ToolCall.Input ([]byte) — still share backing storage with msgs. Neither
// side may mutate a shared element's ToolCalls or write into its Input in
// place; doing so would corrupt the other. Build a new Message instead.
// (Mirrors the memory.Read independent-copy contract.)
type Compactor interface {
	Compact(ctx context.Context, msgs []gantry.Message, budget Budget) ([]gantry.Message, error)
}

// Budget describes the constraints the Compactor should honor.
//
// MaxTokens is the hard prompt-size target; SoftLimit is the size below which
// a strategy may skip compaction. Force means the caller needs the result to
// be smaller than MaxTokens regardless of SoftLimit (set by the overflow
// retry after the provider rejected the prompt as too long). Counter is the
// per-message token estimator; if nil, a default (bytes/4 over content, tool
// calls and IDs, plus a per-message overhead) is used.
type Budget struct {
	MaxTokens int
	SoftLimit int
	Force     bool
	Counter   func(gantry.Message) int
}

// perMessageOverhead approximates the role/framing tokens providers add to
// every message.
const perMessageOverhead = 4

// Count returns the number of tokens for m, using Budget.Counter or a
// default approximation.
func (b Budget) Count(m gantry.Message) int {
	if b.Counter != nil {
		return b.Counter(m)
	}
	n := len(m.Content) + len(m.ToolCallID) + len(m.Name)
	for _, tc := range m.ToolCalls {
		n += len(tc.ID) + len(tc.Name) + len(tc.Input)
	}
	return bytesToTokens(n) + perMessageOverhead
}

// bytesToTokens is the shared ~4-bytes-per-token approximation.
func bytesToTokens(n int) int { return (n + 3) / 4 }
