// Package compactor defines the Compactor interface and reference
// implementations for trimming/summarizing conversation history before
// the LLM call.
package compactor

import (
	"context"

	"github.com/farazhassan/gantry/harness"
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
	Compact(ctx context.Context, msgs []harness.Message, budget Budget) ([]harness.Message, error)
}

// Budget describes the constraints the Compactor should honor.
// Counter is the token-counting function; if nil, a default (4 chars/token)
// is used.
type Budget struct {
	MaxTokens int
	SoftLimit int
	Counter   func(harness.Message) int
}

// Count returns the number of tokens for m, using Budget.Counter or a
// default approximation.
func (b Budget) Count(m harness.Message) int {
	if b.Counter != nil {
		return b.Counter(m)
	}
	// Default: roughly 4 characters per token.
	return (len(m.Content) + 3) / 4
}
