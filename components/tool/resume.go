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
func Resume(ctx context.Context, agent *gantry.Agent, reg *Registry, state *gantry.State, answers []gantry.ToolResult) (*gantry.State, error) {
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
			remaining = append(remaining, calls...)
			continue
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

		t, found := reg.Lookup(entry.ToolName)
		if !found {
			return state, fmt.Errorf("tool: Resume: unknown tool %q for pending call %q", entry.ToolName, origin)
		}
		rt, ok := t.(ResumableTool)
		if !ok {
			return state, fmt.Errorf("tool: Resume: tool %q does not implement ResumableTool", entry.ToolName)
		}

		out, err := rt.Resume(ctx, entry.Resume, group)
		delete(entries, origin)
		if err != nil {
			var pending *gantry.PendingResult
			if errors.As(err, &pending) {
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

	state.PendingToolCalls = remaining
	setPendingEntries(state, entries)

	if len(state.PendingToolCalls) == 0 {
		state.Done = false
		state.DoneReason = ""
		return agent.Resume(ctx, state)
	}
	return state, nil
}
