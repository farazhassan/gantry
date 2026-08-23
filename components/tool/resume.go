// components/tool/resume.go
package tool

import (
	"context"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
)

// Resume fulfills pending calls left in state.PendingToolCalls by a prior
// suspend (state.DoneReason == gantry.DoneClientToolCall) and continues the
// run. Each answer's CallID must match a pending call's ID exactly as
// surfaced there — both plain, declared-client-tool IDs and the
// path-prefixed IDs a ResumableTool's PendingResult produces are handled
// uniformly; callers must treat every ID as an opaque token and never
// construct or parse one themselves.
//
// answers need not cover every currently-pending call: whatever is left
// unanswered simply stays in the returned state.PendingToolCalls, still
// suspended, ready for a later Resume call — so several pending calls from
// one suspend can be resolved incrementally across multiple rounds.
//
// reg must contain every ResumableTool reachable from state's pending calls
// (tool.New callers already retain their Registry for this;
// tool.FromTools/subagent.Component callers should use
// tool.NewWithPolicy/subagent.ComponentWithRegistry instead if they intend
// to call Resume). reg may be nil, or omit tools, when none of the pending
// calls trace back to a ResumableTool (e.g. a plain tool.Client suspend).
//
// A state with no PendingToolCalls at all is returned unchanged (no-op) —
// mirroring gantry.Agent.Resume's own terminal-state no-op contract — since
// there is nothing this call could possibly resolve.
func Resume(ctx context.Context, agent *gantry.Agent, reg *Registry, state *gantry.State, answers []gantry.ToolResult) (*gantry.State, error) {
	if len(state.PendingToolCalls) == 0 {
		return state, nil
	}

	// Validate answers against the actual pending-call ID set up front,
	// before any processing/commit happens: an answer whose CallID doesn't
	// match any currently-pending call, or two answers sharing the same
	// CallID, are both caller mistakes that must be surfaced clearly rather
	// than silently dropped/overwritten by the byID map built below.
	pendingIDs := make(map[string]bool, len(state.PendingToolCalls))
	for _, call := range state.PendingToolCalls {
		pendingIDs[call.ID] = true
	}
	seenAnswer := make(map[string]bool, len(answers))
	for _, ans := range answers {
		if !pendingIDs[ans.CallID] {
			return state, fmt.Errorf("tool: Resume: answer CallID %q does not match any pending call", ans.CallID)
		}
		if seenAnswer[ans.CallID] {
			return state, fmt.Errorf("tool: Resume: duplicate answer CallID %q", ans.CallID)
		}
		seenAnswer[ans.CallID] = true
	}

	entries := pendingEntriesFrom(state)
	if entries == nil {
		entries = map[string]pendingEntry{}
	}

	byID := make(map[string]gantry.ToolResult, len(answers))
	for _, ans := range answers {
		byID[ans.CallID] = ans
	}

	var flat []gantry.ToolCall
	nestedByOrigin := map[string][]gantry.ToolCall{}
	var originOrder []string
	for _, call := range state.PendingToolCalls {
		origin, _, nested := splitPendingID(call.ID)
		if !nested {
			flat = append(flat, call)
			continue
		}
		if _, seen := nestedByOrigin[origin]; !seen {
			originOrder = append(originOrder, origin)
		}
		nestedByOrigin[origin] = append(nestedByOrigin[origin], call)
	}

	var remaining []gantry.ToolCall

	// commit writes whatever progress has accumulated so far in remaining
	// and entries into state, so that any exit path — success or error —
	// leaves already-resolved origins reflected in state.PendingToolCalls
	// and the Meta pending-entries stash. Without this, an error return
	// partway through the nested-call loop would silently discard progress
	// made on origins already processed this call, causing a retry to
	// re-invoke an already-succeeded ResumableTool.Resume and duplicate its
	// side effects and RoleTool message.
	commit := func() {
		state.PendingToolCalls = remaining
		setPendingEntries(state, entries)
	}

	// Flat calls: fulfill by direct message injection — the manual pattern
	// this supersedes — or leave pending if unanswered this round.
	for _, call := range flat {
		ans, ok := byID[call.ID]
		if !ok {
			remaining = append(remaining, call)
			continue
		}
		state.Messages = append(state.Messages, gantry.Message{
			Role:       gantry.RoleTool,
			ToolCallID: call.ID,
			Content:    ans.Content,
		})
	}

	// Nested calls: one group per originating ResumableTool call. A group is
	// only handed to Resume once every entry in it has an answer this round
	// — a partial group stays pending untouched, so a multi-item suspend can
	// be answered incrementally too.
	for _, origin := range originOrder {
		calls := nestedByOrigin[origin]
		entry, ok := entries[origin]
		if !ok {
			// state.Meta's pending-entries stash is out of sync with
			// state.PendingToolCalls (e.g. lossy checkpoint projection, or
			// hand-constructed state) — this call has no continuation
			// metadata to route it. Unlike the "not answered yet this round"
			// case, retrying forever will never resolve it, so this must be
			// a clear error instead of silently re-adding it to remaining
			// and leaving the run suspended forever with no diagnostic.
			remaining = append(remaining, calls...)
			commit()
			return state, fmt.Errorf("tool: Resume: pending call %q has no continuation metadata (lost or corrupted state.Meta?)", origin)
		}

		group := make([]gantry.ToolResult, 0, len(calls))
		complete := true
		for _, call := range calls {
			_, leaf, _ := splitPendingID(call.ID)
			ans, ok := byID[call.ID]
			if !ok {
				complete = false
				break
			}
			group = append(group, gantry.ToolResult{CallID: leaf, Content: ans.Content, IsError: ans.IsError})
		}
		if !complete {
			remaining = append(remaining, calls...)
			continue
		}

		if reg == nil {
			remaining = append(remaining, calls...)
			commit()
			return state, fmt.Errorf("tool: Resume: pending call %q requires a non-nil Registry (got nil)", origin)
		}
		t, found := reg.Lookup(entry.ToolName)
		if !found {
			remaining = append(remaining, calls...)
			commit()
			return state, fmt.Errorf("tool: Resume: unknown tool %q for pending call %q", entry.ToolName, origin)
		}
		rt, ok := t.(ResumableTool)
		if !ok {
			remaining = append(remaining, calls...)
			commit()
			return state, fmt.Errorf("tool: Resume: tool %q does not implement ResumableTool", entry.ToolName)
		}

		out, err := rt.Resume(ctx, entry.Resume, group)
		delete(entries, origin)
		if err != nil {
			var pending *gantry.PendingResult
			// pending != nil guards against a buggy ResumableTool doing
			// `var pr *gantry.PendingResult; return out, pr` — errors.As
			// still matches and would leave pending nil, and treating that
			// as a genuine suspend signal would panic on pending.Pending
			// below. Fall through to the normal error path instead, same as
			// any other non-PendingResult error here.
			if errors.As(err, &pending) && pending != nil {
				if len(pending.Pending) > 0 {
					entries[origin] = pendingEntry{ToolName: entry.ToolName, Resume: pending.Resume}
					for _, p := range pending.Pending {
						remaining = append(remaining, gantry.ToolCall{
							ID:    origin + pendingIDSep + p.ID,
							Name:  p.Name,
							Input: p.Input,
						})
					}
					continue
				}
				// Pending is empty: "suspend with nothing to wait for" is a
				// contradiction (see gantry.PendingResult's doc comment) —
				// this origin is not really pending again, so fold a
				// normal tool-error message instead of leaving it
				// suspended (and entries already has origin deleted above,
				// so no stale continuation token is left behind).
				err = pendingResultContractErr(entry.ToolName)
			}
			state.Messages = append(state.Messages, gantry.Message{
				Role:       gantry.RoleTool,
				ToolCallID: origin,
				Content:    err.Error(),
			})
			continue
		}

		state.Messages = append(state.Messages, gantry.Message{
			Role:       gantry.RoleTool,
			ToolCallID: origin,
			Content:    string(out),
		})
	}

	commit()

	if len(state.PendingToolCalls) == 0 {
		state.Done = false
		state.DoneReason = ""
		return agent.Resume(ctx, state)
	}
	return state, nil
}
