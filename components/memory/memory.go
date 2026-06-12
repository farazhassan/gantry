// Package memory defines the Memory interface for persisting conversation
// history and provides an in-memory reference implementation.
//
// Middleware ordering: components that do work after next() on PhasePostLLM
// (memory persist, critic, limiter finalize) run that work in forward
// registration order — the last-registered middleware is the outermost and so
// runs last. Register WithMemory last so memory:persist observes the finalized
// turn: after the critic has rewritten (Verdict.ModifyOutput) or rejected
// (Verdict.Accept == false) the assistant message.
package memory

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// Memory persists and reads back the conversation transcript.
type Memory interface {
	Append(ctx context.Context, msg harness.Message) error

	// Read returns the stored transcript as a fresh slice that does not alias
	// the store's backing array: callers may retain, reslice, reorder, or
	// replace whole elements without affecting the store or other readers.
	//
	// Independence is at the slice level only. The returned Message values are
	// shallow copies; reference-typed fields — notably ToolCalls and each
	// ToolCall.Input ([]byte) — still share backing storage with the store.
	// Treat returned messages as read-only: do NOT mutate a returned element's
	// ToolCalls or write into its Input in place, as that would corrupt the
	// stored transcript. Build a new Message instead.
	Read(ctx context.Context) ([]harness.Message, error)
}
