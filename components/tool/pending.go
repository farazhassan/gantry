// components/tool/pending.go
package tool

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

const (
	// pendingResumeMetaKey namespaces the per-run stash of continuation
	// state for tool-pending calls (see pendingEntry), following the
	// State.Meta convention.
	pendingResumeMetaKey = "components/tool:pending_resume"

	// pendingIDSep joins an originating call's ID to each of its
	// *gantry.PendingResult.Pending items' own IDs, producing the composite
	// ID surfaced in PendingToolCalls. Chosen to be safe against real
	// provider-generated tool-call IDs (typically alphanumeric plus
	// underscore/hyphen). Callers must treat every surfaced ID as opaque —
	// never construct or parse one themselves; components/tool.Resume does
	// that internally.
	pendingIDSep = "\x1f"
)

// pendingEntry is the per-call bookkeeping the suspend middleware stashes so
// a later tool.Resume can route an answer to the right ResumableTool. JSON
// tags because it round-trips through State.Meta (map[string]any),
// including across a checkpoint save/load.
//
// Partial accumulates already-answered leaves (keyed by leaf CallID, i.e.
// the part of the composite ID after the origin) for a nested group that
// isn't complete yet, so a caller can answer a multi-item group
// incrementally across several Resume calls without ever needing to
// re-supply an answer already given. It's omitempty so the common case (no
// partial answers yet — including every flat, declared-client-tool pending
// call, which never uses this field at all) keeps the same JSON shape older
// persisted entries had.
type pendingEntry struct {
	ToolName string                       `json:"tool_name"`
	Resume   json.RawMessage              `json:"resume"`
	Partial  map[string]gantry.ToolResult `json:"partial,omitempty"`
}

// pendingEntriesFrom reads the pending-call stash from s.Meta, or nil if
// none is present. The value may be the original map[string]pendingEntry
// (same-process, never serialized) or a generic map[string]interface{} (has
// been through a JSON round-trip, e.g. a checkpoint save/load) — re-marshal
// then re-unmarshal works uniformly for either, since JSON is idempotent
// under that operation.
func pendingEntriesFrom(s *gantry.State) map[string]pendingEntry {
	raw, ok := s.Meta[pendingResumeMetaKey]
	if !ok {
		return nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var m map[string]pendingEntry
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// setPendingEntries replaces the pending-call stash on s.Meta wholesale — it
// does not merge m into whatever was there before. Callers must ensure that
// any entry omitted from m is already resolved, not merely absent from the
// current pass: the suspend middleware in client.go relies on this by
// calling setPendingEntries with only the entries found in that PhaseObserve
// pass, which is safe only once tool.Resume (a later task) establishes the
// invariant that a resolved entry is removed from the stash rather than
// carried forward unresolved.
func setPendingEntries(s *gantry.State, m map[string]pendingEntry) {
	if len(m) == 0 {
		if s.Meta != nil {
			delete(s.Meta, pendingResumeMetaKey)
		}
		return
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	s.Meta[pendingResumeMetaKey] = m
}

// splitPendingID splits a composite pending ID at its first pendingIDSep
// into the originating call's ID and everything after it (which may itself
// still be composite, for more than one level of nesting). nested is false
// for a plain ID (a declared client-tool call, or any ID with no separator).
func splitPendingID(id string) (origin, leaf string, nested bool) {
	i := strings.Index(id, pendingIDSep)
	if i < 0 {
		return "", "", false
	}
	return id[:i], id[i+len(pendingIDSep):], true
}

// pendingResultContractErr returns the error to fold as a plain tool error
// in place of a *gantry.PendingResult whose Pending is empty — "suspend
// with nothing to wait for" is a contradiction (see
// gantry.PendingResult's doc comment), so there is no sensible way to
// actually suspend on it. Shared by middleware.go's dispatch (a fresh
// Tool.Invoke returning *gantry.PendingResult) and resume.go's re-suspend
// branch (a ResumableTool.Resume call returning one) — the two places a
// *gantry.PendingResult is produced directly from tool code, before it
// could otherwise reach SuspendClientCalls's stripping logic.
func pendingResultContractErr(toolName string) error {
	return fmt.Errorf("tool: %s returned a PendingResult with no Pending calls (nothing to suspend on)", toolName)
}

// pendingIDSepContractErr returns the error to fold as a plain tool error
// when the originating call's own CallID already contains pendingIDSep —
// the separator SuspendClientCalls uses to build the composite ID (origin +
// pendingIDSep + leaf ID) surfaced in PendingToolCalls. Every ID is
// documented as opaque (see pendingIDSep's doc comment), but nothing stops a
// provider-generated tool-call ID from containing it anyway; building the
// composite ID from it would corrupt the scheme, since splitPendingID only
// ever recovers the prefix up to the FIRST separator as the origin — the
// entries map (keyed by the real, full CallID) would then be unreachable
// from the surfaced composite ID. Folding this as a normal tool error here,
// at the point the composite ID would be constructed, gives a much clearer
// diagnostic than the generic "no continuation metadata" error a misparsed
// ID would eventually produce downstream.
//
// Deliberately NOT applied to a PendingResult.Pending[i].ID containing the
// separator: multi-level nesting (an agent-as-tool delegate whose own child
// already suspended) legitimately passes an already-composite child ID
// straight through as Pending[i].ID — see splitPendingID's doc comment
// ("leaf ... may itself still be composite, for more than one level of
// nesting") and components/subagent's delegateTool.asResult, which does
// exactly that. Each level only ever splits once and forwards its own
// residual leaf opaquely to the level below, so an embedded separator there
// causes no misparse.
//
// This asymmetry means a non-delegating ResumableTool's own terminal
// Pending[i].ID — one not itself forwarded from a nested ResumableTool's
// suspend — is NOT validated anywhere in this package: a delegating tool's
// legitimately-composite forwarded child ID and a non-delegating tool's
// pathologically separator-containing ID are indistinguishable strings at
// this generic middleware, and rejecting the former would break legitimate
// nesting. Only the tool that owns a terminal ID knows whether it is meant
// to be composite, so guarding against this (a non-conforming LLM
// provider/gateway or a buggy tool author supplying a non-alphanumeric ID —
// see pendingIDSep's doc comment; real provider IDs never do this) is each
// non-delegating ResumableTool's own responsibility, not something
// components/tool can centrally enforce.
func pendingIDSepContractErr(toolName, badID string) error {
	return fmt.Errorf("tool: %s's PendingResult originating call ID %q contains the reserved pending-ID separator %q; tool-call IDs must not contain it", toolName, badID, pendingIDSep)
}

// pendingIDSepClientCallErr is pendingIDSepContractErr's counterpart for a
// declared client-tool call (tool.Client/tool.DynamicClient) whose own ID
// already contains pendingIDSep, caught in client.go's SuspendClientCalls
// before the call is ever surfaced in PendingToolCalls. Unlike a
// PendingResult's Pending[i].ID (see pendingIDSepContractErr's doc comment
// above), a declared client-tool call's ID is always this level's own
// freshly-dispatched LLM tool_call ID — never itself composite, and never
// legitimately forwarded from a nested suspend — so, like the originating
// CallID pendingIDSepContractErr guards, it is safe to reject centrally
// here.
func pendingIDSepClientCallErr(toolName, badID string) error {
	return fmt.Errorf("tool: declared client tool %s's call ID %q contains the reserved pending-ID separator %q; tool-call IDs must not contain it", toolName, badID, pendingIDSep)
}

// toolNameFor looks up the Name of the pending call with the given ID in
// calls — used by the suspend middleware, before DefaultObserveHandler
// clears state.PendingToolCalls, to record which tool a pending result came
// from.
func toolNameFor(calls []gantry.ToolCall, callID string) string {
	for _, c := range calls {
		if c.ID == callID {
			return c.Name
		}
	}
	return ""
}
