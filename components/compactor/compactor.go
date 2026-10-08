// Package compactor defines the Compactor interface and reference
// implementations for trimming/summarizing conversation history before
// the LLM call.
//
// NewPolicy is the recommended Compactor: it compacts only once the
// provider-measured prompt reaches a fraction of the context window
// (Policy.Trigger) and then runs a ladder of turn-aware steps —
// ClearToolResults, TruncateMessages, SummarizeTurns, DropTurns, or any
// Compactor — until it is back under Policy.Target. SlidingWindow, HeadTail
// and Summarizing are simple count-based strategies.
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

// Budget describes what the caller asks the Compactor to aim for. It is a
// request, not a guarantee: strategies decide how much of it they honour.
//
// MaxTokens is the requested prompt-size target for Messages; SoftLimit is the
// size below which a strategy may skip compaction. Force asks the strategy to
// try to compact further than it otherwise would, ignoring SoftLimit (set by
// the overflow handler after the provider rejected the prompt as too long).
// The built-in SlidingWindow and HeadTail ignore all three (they trim by
// message count); Summarizing uses SoftLimit and Force but does not check its
// result against MaxTokens. Counter is the per-message token estimator; if
// nil, a default (bytes/4 over content, tool calls and IDs, plus a
// per-message overhead) is used.
//
// ContextWindow, PromptTokens and FixedTokens describe the request the
// compacted Messages will be part of. The New middleware fills them from
// State before every Compact call; they are zero when Compact is called
// directly. ContextWindow is State.ContextWindow (0 = unknown). PromptTokens
// is EstimatePromptTokens: the provider-measured prompt size plus an estimate
// of messages added since, for the whole prompt. FixedTokens estimates the
// part Compact cannot shrink: System, Tools and any messages held out of
// Compact (the max-iterations wrap-up prompt and what follows it).
type Budget struct {
	MaxTokens int
	SoftLimit int
	Force     bool
	Counter   func(gantry.Message) int

	ContextWindow int
	PromptTokens  int
	FixedTokens   int
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
