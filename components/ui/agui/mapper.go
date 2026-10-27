package agui

import (
	"fmt"
	"time"

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

	// liveResultSeen tracks "runID:callID" for every call already reported
	// via EventToolResultLive, so the later batched EventToolResult for the
	// same call (see run_stream.go's emitPhaseEffects, which reports the
	// whole PhaseToolExec batch regardless of whether any of its calls were
	// already live-reported) is suppressed instead of delivering a second,
	// redundant TOOL_CALL_RESULT for the same toolCallId.
	liveResultSeen map[string]bool

	// callStarted tracks "runID:callID" for every call already opened with
	// TOOL_CALL_START from an EventToolCallStart, so the call's later
	// EventToolCall sends only its TOOL_CALL_ARGS and TOOL_CALL_END instead
	// of starting the same toolCallId twice.
	callStarted map[string]bool

	// subagentStarted tracks, per gantry RunID, whether SUBAGENT_STARTED has
	// already been emitted for that (nested) run -- mirrors started's
	// lazy-once pattern for RUN_STARTED, but keyed per-RunID since a Mapper
	// sees many nested runs over its lifetime.
	subagentStarted map[string]bool

	// resetState makes startFrame follow RUN_STARTED with STATE_SNAPSHOT {}
	// — see WithRunStateReset.
	resetState bool
}

// NewMapper returns a Mapper for a single AG-UI thread identified by
// threadID/runID (the AG-UI-level ids — a different id space from any
// Gantry-level Event.RunID), configured by opts (see WithRunStateReset).
func NewMapper(threadID, runID string, opts ...MapperOption) *Mapper {
	m := &Mapper{
		threadID:        threadID,
		runID:           runID,
		openMsg:         map[string]string{},
		msgSeq:          map[string]int{},
		openReasoning:   map[string]string{},
		lastUsage:       map[string]gantry.Usage{},
		lastActivity:    map[string]activityStepValue{},
		subagentStarted: map[string]bool{},
		liveResultSeen:  map[string]bool{},
		callStarted:     map[string]bool{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	return m
}

// subagentErrorReasons are the DoneReasons that classify a nested run's
// EventDone as SUBAGENT_ERROR rather than SUBAGENT_FINISHED. DoneHandoff is
// included because errors.go already documents task-driven runs treating a
// handoff as an explicit failure -- there is no resolver concept a
// delegate-tool child could act on, so the same treatment applies here.
// DoneClientToolCall is deliberately excluded: it's a suspend (see
// SUBAGENT_FINISHED's outcome:"suspended"), not a failure.
//
// If you add a new gantry.DoneReason to errors.go, you MUST also make a
// conscious classification decision here: either add it to this map (it's a
// failure, like the reasons above), or leave it out with a comment
// explaining why it belongs in the SUBAGENT_FINISHED/success bucket instead
// (like DoneClientToolCall above). TestSubagentErrorReasonsExactSet in
// mapper_test.go only guards this map's own contents against accidental
// drift (an entry added or removed by mistake) -- it cannot detect a new
// DoneReason value that exists in errors.go but was never considered here,
// since Go has no reflection over a const block. Left unclassified, a new
// reason silently falls through Map's default case and is reported as
// SUBAGENT_FINISHED (success) with no compiler or test signal.
var subagentErrorReasons = map[gantry.DoneReason]bool{
	gantry.DoneError:             true,
	gantry.DoneGuardrailBlocked:  true,
	gantry.DoneHumanAborted:      true,
	gantry.DoneToolPolicyAborted: true,
	gantry.DoneHandoff:           true,
}

// Map translates one Gantry event into zero-or-more AG-UI events. It emits
// RUN_STARTED lazily before the first translated event, attaches Gantry
// identity (RunID/Agent/ParentToolCallID/...) to every translated event
// except the three lifecycle events, and unconditionally prepends any
// metadata-like CUSTOM events (events_dropped, usage) the event carries —
// these, like RAW, ACTIVITY_* and STATE_SNAPSHOT, are allowed to interleave
// inside an open text/reasoning message; AG-UI's message framing is keyed by
// messageId, so an unrelated CUSTOM/RAW/ACTIVITY/STATE event between a
// message's Start and End is valid. Only EventTextDelta and
// EventReasoningDelta close the OTHER kind's still-open message (with
// TEXT_MESSAGE_END / REASONING_MESSAGE_END + REASONING_END) before opening
// their own — text and reasoning are mutually exclusive per run, so a client
// never sees interleaved content within a single message — and
// EventToolCallStart/EventToolCall/EventToolResult/EventToolResultLive/
// EventPhaseStart/EventPhaseEnd/EventDone close both, since those events
// represent gantry moving past whatever the model was saying/thinking.
// EventToolCallStart opens the call with TOOL_CALL_START alone; the same
// call's EventToolCall then sends just TOOL_CALL_ARGS and TOOL_CALL_END
// (see the callStarted field).
// EventToolResultLive additionally suppresses the later EventToolResult for
// the same call (see toolResultMsgID and the liveResultSeen field) so a
// client never sees the same tool call's result delivered twice — once in
// real time as the call resolves, and again when its whole PhaseToolExec
// batch finishes.
//
// EventStateChanged (gantry.SetState) maps to STATE_SNAPSHOT only for the
// top-level run; a nested run's is dropped (see the case below).
//
// EventDone is translated based on whether it came from the top-level run
// or a nested one: a done event with empty ParentRunID/ParentToolCallID is
// the top-level run finishing, and maps unconditionally to RUN_FINISHED
// (ending the whole AG-UI stream); a done event WITH a parent link is a
// nested sub-agent run finishing — the AG-UI stream, and the parent run,
// may still be going — and maps to either SUBAGENT_FINISHED or
// SUBAGENT_ERROR depending on DoneReason (see subagentErrorReasons for the
// exact classification), with SUBAGENT_FINISHED additionally carrying
// outcome:{type:"suspended"} when the reason is DoneClientToolCall.
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
	if ev.ParentRunID != "" || ev.ParentToolCallID != "" {
		id.SubagentRunID = ev.RunID
	}
	out = append(out, m.subagentStartFrame(ev, id)...)

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

	case gantry.EventPlanStepChanged:
		if ev.PlanStep != nil {
			out = append(out, m.activityChange(ev.RunID, *ev.PlanStep, id)...)
		}

	case gantry.EventStateChanged:
		// Shared state belongs to the AG-UI thread, which only the
		// top-level run owns: a nested sub-agent run's state_changed
		// (parent link set — the same test subagentStartFrame and the
		// EventDone case use) is dropped instead of overwriting the
		// thread's state with a sub-agent's private snapshot.
		if ev.ParentRunID == "" && ev.ParentToolCallID == "" {
			out = append(out, newStateSnapshot(ev.State).withIdentity(id))
		}

	case gantry.EventToolCallStart:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tc := ev.ToolCall; tc != nil {
			key := ev.RunID + ":" + tc.ID
			if !m.callStarted[key] {
				m.callStarted[key] = true
				out = append(out, newToolCallStart(tc.ID, tc.Name).withIdentity(id))
			}
		}

	case gantry.EventToolCall:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tc := ev.ToolCall; tc != nil {
			key := ev.RunID + ":" + tc.ID
			if m.callStarted[key] {
				delete(m.callStarted, key)
			} else {
				out = append(out, newToolCallStart(tc.ID, tc.Name).withIdentity(id))
			}
			out = append(out,
				newToolCallArgs(tc.ID, string(tc.Input)).withIdentity(id),
				newToolCallEnd(tc.ID).withIdentity(id),
			)
		}

	case gantry.EventToolResultLive:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tr := ev.ToolResult; tr != nil {
			m.liveResultSeen[ev.RunID+":"+tr.CallID] = true
			out = append(out, newToolCallResult(toolResultMsgID(ev.RunID, tr.CallID), tr.CallID, tr.Content, tr.IsError).withIdentity(id))
		}

	case gantry.EventToolResult:
		out = append(out, m.closeText(ev.RunID, id)...)
		out = append(out, m.closeReasoning(ev.RunID, id)...)
		if tr := ev.ToolResult; tr != nil {
			key := ev.RunID + ":" + tr.CallID
			if m.liveResultSeen[key] {
				delete(m.liveResultSeen, key)
			} else {
				out = append(out, newToolCallResult(toolResultMsgID(ev.RunID, tr.CallID), tr.CallID, tr.Content, tr.IsError).withIdentity(id))
			}
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
		switch {
		case ev.ParentRunID == "" && ev.ParentToolCallID == "":
			out = append(out, newRunFinished(m.threadID, m.runID))
		case subagentErrorReasons[ev.DoneReason]:
			msg := "sub-agent run terminated: " + string(ev.DoneReason)
			if ev.Error != "" {
				msg += ": " + ev.Error
			}
			out = append(out, newSubagentError(ev.RunID, msg).withIdentity(id))
		default:
			out = append(out, newSubagentFinished(ev.RunID, ev.FinalOutput, ev.DoneReason == gantry.DoneClientToolCall).withIdentity(id))
		}
	}

	return stampTimestamps(out, ev.Timestamp)
}

// stampTimestamps returns events with every event's AG-UI "timestamp" set to
// t's milliseconds-since-epoch value, or events unchanged if t is the zero
// time (an Event constructed directly without going through gantry's emit()
// chokepoint, e.g. in tests) — leaving Timestamp at its zero value so
// encoding/json's omitempty drops the field, matching AG-UI's optional
// timestamp semantics instead of emitting a bogus pre-1970 value.
func stampTimestamps(events []Event, t time.Time) []Event {
	if t.IsZero() {
		return events
	}
	ms := t.UnixMilli()
	for i, e := range events {
		events[i] = e.withTimestamp(ms)
	}
	return events
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
	return []Event{newUsage(usageValue{
		InputTokens:      ev.Usage.InputTokens,
		OutputTokens:     ev.Usage.OutputTokens,
		CacheReadTokens:  ev.Usage.CacheReadTokens,
		CacheWriteTokens: ev.Usage.CacheWriteTokens,
		CostUSD:          ev.Usage.Cost,
	}, id)}
}

// startFrame returns a RUN_STARTED event the first time it is called, marking
// the run started; later calls return nil. Map uses it lazily, and the Sink
// uses it so a RUN_ERROR emitted before any Gantry event is still preceded by
// RUN_STARTED. With WithRunStateReset, RUN_STARTED is followed by
// STATE_SNAPSHOT {} (no identity: no Gantry event produced it) on both
// paths.
func (m *Mapper) startFrame() []Event {
	if m.started {
		return nil
	}
	m.started = true
	out := []Event{newRunStarted(m.threadID, m.runID)}
	if m.resetState {
		out = append(out, newStateSnapshot(map[string]any{}))
	}
	return out
}

// subagentStartFrame returns a SUBAGENT_STARTED event the first time Map
// sees ev's RunID as a nested (sub-agent) run, marking it started so later
// events for the same RunID return nil -- mirrors startFrame's lazy-once
// pattern for RUN_STARTED. Returns nil for a top-level event (no
// ParentRunID/ParentToolCallID set).
//
// parentSubagentRunID's m.subagentStarted[ev.ParentRunID] check relies on
// the parent run's own first event always being processed before any event
// from a run it spawns -- true by causality (a run must itself have
// started before it can invoke a tool that spawns a child), not something
// Sink's mutex itself orders (that mutex only serializes concurrent Map
// calls, it doesn't order them across goroutines).
func (m *Mapper) subagentStartFrame(ev gantry.Event, id identity) []Event {
	if ev.ParentRunID == "" && ev.ParentToolCallID == "" {
		return nil
	}
	if m.subagentStarted[ev.RunID] {
		return nil
	}
	m.subagentStarted[ev.RunID] = true
	var parentSubagentRunID string
	if m.subagentStarted[ev.ParentRunID] {
		parentSubagentRunID = ev.ParentRunID
	}
	return []Event{newSubagentStarted(ev.RunID, ev.ParentToolName, ev.ParentToolDescription, parentSubagentRunID).withIdentity(id)}
}

// toolResultMsgID returns the AG-UI messageId for a TOOL_CALL_RESULT, shared
// by the EventToolResultLive and EventToolResult cases so a live result and
// the (suppressed) batched one it stands in for would have produced the
// identical id.
func toolResultMsgID(runID, callID string) string {
	return fmt.Sprintf("%s:toolmsg:%s", runID, callID)
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

// activityChange translates a changed gantry.PlanStep into an AG-UI
// ACTIVITY_SNAPSHOT (the first time this Mapper has reported this step for
// this run) or an ACTIVITY_DELTA (a JSON Patch against the last snapshot
// sent for that step) — the snapshot/delta split AG-UI defines for activity
// messages. Returns nil if the step's tracked fields haven't actually
// changed since the last report (e.g. a redundant emit).
func (m *Mapper) activityChange(runID string, step gantry.PlanStep, id identity) []Event {
	key := runID + ":" + step.ID
	msgID := runID + ":activity:" + step.ID
	cur := activityStepValue{ID: step.ID, Description: step.Description, Status: string(step.Status), Output: step.Output}

	last, seen := m.lastActivity[key]
	m.lastActivity[key] = cur
	if !seen {
		return []Event{newActivitySnapshot(msgID, cur).withIdentity(id)}
	}
	patch := diffActivity(last, cur)
	if len(patch) == 0 {
		return nil
	}
	return []Event{newActivityDelta(msgID, patch).withIdentity(id)}
}

// diffActivity returns the RFC 6902 JSON Patch operations turning prev into
// cur, one "replace" per changed field, in a fixed status/description/output
// order. Nil if prev == cur.
func diffActivity(prev, cur activityStepValue) []jsonPatchOp {
	var ops []jsonPatchOp
	if prev.Status != cur.Status {
		ops = append(ops, jsonPatchOp{Op: "replace", Path: "/status", Value: cur.Status})
	}
	if prev.Description != cur.Description {
		ops = append(ops, jsonPatchOp{Op: "replace", Path: "/description", Value: cur.Description})
	}
	if prev.Output != cur.Output {
		ops = append(ops, jsonPatchOp{Op: "replace", Path: "/output", Value: cur.Output})
	}
	return ops
}
