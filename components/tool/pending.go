// components/tool/pending.go
package tool

import (
	"encoding/json"
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
type pendingEntry struct {
	ToolName string          `json:"tool_name"`
	Resume   json.RawMessage `json:"resume"`
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
