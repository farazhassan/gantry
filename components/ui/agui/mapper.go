package agui

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// Mapper translates Gantry's whole-run Event stream into AG-UI protocol
// events. It tracks whether RUN_STARTED has been emitted, and — per Gantry
// RunID, not globally — whether a text message is currently open, so text
// from an interleaved parent and sub-agent run brackets independently
// instead of corrupting a shared "open message" slot. Use one Mapper per
// AG-UI thread (HTTP request), not per Gantry run: with sub-agent
// passthrough enabled, one Mapper legitimately sees events from several
// Gantry runs (the parent's own, plus any nested sub-agent runs) on the
// same stream. Mapper itself is not safe for concurrent Map calls — see
// Sink, which serializes calls into Map with a mutex.
type Mapper struct {
	threadID string
	runID    string
	started  bool // RUN_STARTED emitted?

	openMsg map[string]string // gantry RunID -> open messageId, if any
	msgSeq  map[string]int    // gantry RunID -> monotonic counter for that run, shared across text and reasoning messages

	openReasoning map[string]string // gantry RunID -> open reasoning messageId, if any

	lastUsage    map[string]gantry.Usage      // gantry RunID -> last usage snapshot emitted as gantry.usage, if any
	lastActivity map[string]activityStepValue // "runID:stepID" -> last activity snapshot/delta content emitted for that step
}

// NewMapper returns a Mapper for a single AG-UI thread identified by
// threadID/runID (the AG-UI-level ids — a different id space from any
// Gantry-level Event.RunID).
func NewMapper(threadID, runID string) *Mapper {
	return &Mapper{
		threadID:      threadID,
		runID:         runID,
		openMsg:       map[string]string{},
		msgSeq:        map[string]int{},
		openReasoning: map[string]string{},
		lastUsage:     map[string]gantry.Usage{},
		lastActivity:  map[string]activityStepValue{},
	}
}

// Map translates one Gantry event into zero-or-more AG-UI events. It emits
// RUN_STARTED lazily before the first translated event, attaches Gantry
// identity (RunID/Agent/ParentToolCallID/...) to every translated event
// except the three lifecycle events, and closes any of ev.RunID's open text
// or reasoning messages (with TEXT_MESSAGE_END / REASONING_MESSAGE_END +
// REASONING_END) before emitting a non-text, non-reasoning event for that
// run. Text and reasoning messages are mutually exclusive per run: opening
// one closes the other's still-open message first, so a client never sees
// interleaved content within a single message.
//
// EventDone is translated based on whether it came from the top-level run
// or a nested one: a done event with empty ParentRunID/ParentToolCallID is
// the top-level run finishing, and maps to RUN_FINISHED (ending the whole
// AG-UI stream); a done event WITH a parent link is a nested sub-agent run
// finishing, and maps to a CUSTOM "gantry.subagent_done" event instead — the
// AG-UI stream, and the parent run, may still be going.
func (m *Mapper) Map(ev gantry.Event) []Event {
	out := m.startFrame()
	id := identity{
		RunID:            ev.RunID,
		SessionID:        ev.SessionID,
		TaskID:           ev.TaskID,
		Agent:            ev.Agent,
		ParentRunID:      ev.ParentRunID,
		ParentToolCallID: ev.ParentToolCallID,
	}

	if ev.Dropped > 0 {
		out = append(out, newEventsDropped(ev.Dropped, id))
	}
	out = append(out, m.usageDelta(ev, id)...)

	switch ev.Type {
	case gantry.EventTextDelta:
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		open, hasOpen := m.openMsg[ev.RunID]
		if !hasOpen {
			m.msgSeq[ev.RunID]++
			open = fmt.Sprintf("%s:msg:%d", ev.RunID, m.msgSeq[ev.RunID])
			m.openMsg[ev.RunID] = open
			out = append(out, newTextMessageStart(open).withIdentity(id))
		}
		out = append(out, newTextMessageContent(open, ev.TextDelta).withIdentity(id))

	case gantry.EventReasoningDelta:
		out = append(out, m.closeText(ev.RunID, id)...)
		open, hasOpen := m.openReasoning[ev.RunID]
		if !hasOpen {
			m.msgSeq[ev.RunID]++
			open = fmt.Sprintf("%s:reasoning:%d", ev.RunID, m.msgSeq[ev.RunID])
			m.openReasoning[ev.RunID] = open
			out = append(out,
				newReasoningStart(open).withIdentity(id),
				newReasoningMessageStart(open).withIdentity(id),
			)
		}
		out = append(out, newReasoningMessageContent(open, ev.ReasoningDelta).withIdentity(id))

	case gantry.EventRaw:
		out = append(out, newRaw(ev.RawFrame, ev.RawSource).withIdentity(id))

	case gantry.EventToolCall:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tc := ev.ToolCall; tc != nil {
			out = append(out,
				newToolCallStart(tc.ID, tc.Name).withIdentity(id),
				newToolCallArgs(tc.ID, string(tc.Input)).withIdentity(id),
				newToolCallEnd(tc.ID).withIdentity(id),
			)
		}

	case gantry.EventToolResult:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tr := ev.ToolResult; tr != nil {
			msgID := fmt.Sprintf("%s:toolmsg:%s", ev.RunID, tr.CallID)
			out = append(out, newToolCallResult(msgID, tr.CallID, tr.Content, tr.IsError).withIdentity(id))
		}

	case gantry.EventPhaseStart:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		out = append(out, newStepStarted(stepName(ev)).withIdentity(id))

	case gantry.EventPhaseEnd:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		out = append(out, newStepFinished(stepName(ev)).withIdentity(id))

	case gantry.EventDone:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if ev.ParentRunID == "" && ev.ParentToolCallID == "" {
			out = append(out, newRunFinished(m.threadID, m.runID))
		} else {
			out = append(out, newSubagentDone(id))
		}
	}

	return out
}

// usageDelta returns a gantry.usage CUSTOM event for ev.RunID if ev carries a
// Usage snapshot that differs from the last one emitted for that run,
// updating the tracked value as a side effect; otherwise it returns nil.
// This keeps phase_end/done events — which every gantry run emits, whether
// or not an LLM call actually ran that phase — from spamming a usage event
// on the wire each time, since most phases leave usage unchanged.
func (m *Mapper) usageDelta(ev gantry.Event, id identity) []Event {
	if ev.Usage == nil {
		return nil
	}
	if last, ok := m.lastUsage[ev.RunID]; ok && last == *ev.Usage {
		return nil
	}
	m.lastUsage[ev.RunID] = *ev.Usage
	return []Event{newUsage(ev.Usage.InputTokens, ev.Usage.OutputTokens, ev.Usage.Cost, id)}
}

// startFrame returns a RUN_STARTED event the first time it is called, marking
// the run started; later calls return nil. Map uses it lazily, and the Sink
// uses it so a RUN_ERROR emitted before any Gantry event is still preceded by
// RUN_STARTED.
func (m *Mapper) startFrame() []Event {
	if m.started {
		return nil
	}
	m.started = true
	return []Event{newRunStarted(m.threadID, m.runID)}
}

// stepName returns the AG-UI STEP_STARTED/STEP_FINISHED stepName for ev.
// The top-level run's own phase names are left bare (e.g. "tool_exec") for
// backward compatibility with clients that key off Gantry's well-known
// phase names (e.g. a status-label lookup table). A NESTED run's phase
// names are suffixed with its own RunID to guarantee global uniqueness
// across the whole AG-UI stream: @ag-ui/client's own protocol verifier
// tracks STEP_STARTED/STEP_FINISHED by stepName alone, with no notion of
// "which run" -- without this, a nested run's phase that happens to share
// a name with the top-level run's currently-open phase (e.g. both called
// "tool_exec" at once, which is the common case once a delegate tool's
// child agent starts running its own tools) makes the client throw
// "Step ... is already active" before any application code ever sees the
// event.
func stepName(ev gantry.Event) string {
	if ev.ParentRunID == "" && ev.ParentToolCallID == "" {
		return string(ev.Phase)
	}
	return string(ev.Phase) + "::" + ev.RunID
}

// closeText emits TEXT_MESSAGE_END for runID's open text message, if any,
// and clears that run's open-message state. Returns nil when no message is
// open for runID. id is attached to the END event so its identity matches
// the message it closes.
func (m *Mapper) closeText(runID string, id identity) []Event {
	open, ok := m.openMsg[runID]
	if !ok {
		return nil
	}
	delete(m.openMsg, runID)
	return []Event{newTextMessageEnd(open).withIdentity(id)}
}

// closeReasoning emits REASONING_MESSAGE_END + REASONING_END for runID's open
// reasoning message, if any, and clears that run's open-reasoning state.
// Returns nil when no reasoning message is open for runID. Mirrors closeText.
func (m *Mapper) closeReasoning(runID string, id identity) []Event {
	open, ok := m.openReasoning[runID]
	if !ok {
		return nil
	}
	delete(m.openReasoning, runID)
	return []Event{
		newReasoningMessageEnd(open).withIdentity(id),
		newReasoningEnd(open).withIdentity(id),
	}
}

// closeAllText emits TEXT_MESSAGE_END for every currently-open text message,
// across every Gantry run this Mapper has seen. Sink.EmitError uses this: a
// fatal error ends the whole AG-UI stream, not just one run, and (unlike
// Map) EmitError has no specific gantry.Event to scope the close to, so
// every open run must be closed. Only RunID is known for each closed
// message's identity — the other identity fields aren't retained once a
// run's events stop arriving.
//
// Multiple simultaneously-open runs are not a rare edge case here: it's
// exactly what happens whenever a parent run's text message is still open
// when a nested sub-agent run opens its own (the scenario this whole
// feature targets). Map iteration order is unspecified, so the resulting
// END frames may come out in any relative order — that's harmless, since
// every event self-identifies via its own messageId/RunID and a client
// demuxes by those, not by frame order across runs.
func (m *Mapper) closeAllText() []Event {
	var out []Event
	for runID := range m.openMsg {
		out = append(out, m.closeText(runID, identity{RunID: runID})...)
	}
	return out
}

// closeAllReasoning emits REASONING_MESSAGE_END + REASONING_END for every
// currently-open reasoning message, across every Gantry run this Mapper has
// seen. Mirrors closeAllText; Sink.EmitError calls both.
func (m *Mapper) closeAllReasoning() []Event {
	var out []Event
	for runID := range m.openReasoning {
		out = append(out, m.closeReasoning(runID, identity{RunID: runID})...)
	}
	return out
}
