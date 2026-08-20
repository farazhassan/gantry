package vercelai

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// Mapper translates Gantry's whole-run Event stream into UI Message Stream
// chunks for a single assistant message. Unlike agui's Mapper (one per
// AG-UI thread, which can legitimately see events from several Gantry runs
// via sub-agent passthrough), Mapper here is scoped to exactly one
// top-level Gantry run: an event carrying ParentRunID or ParentToolCallID
// (a nested sub-agent run) is dropped entirely by Map -- see doc.go's
// "Sub-agent scope" note for why the protocol has no run-graph concept to
// attribute such an event to. Mapper is not safe for concurrent Map calls
// -- see Sink, which serializes calls with a mutex.
type Mapper struct {
	messageID string
	started   bool // "start" chunk emitted?

	openText      string // open text-block id, if any
	openReasoning string // open reasoning-block id, if any
	blockSeq      int    // monotonic counter shared by text and reasoning block ids

	stepOpen bool // start-step emitted, finish-step not yet

	lastUsage gantry.Usage
	haveUsage bool

	lastActivity map[string]planStepValue // stepID -> last content emitted for that step
}

// NewMapper returns a Mapper that builds a single assistant message
// identified by messageID (a fresh id per HTTP request/turn -- see the
// "start" chunk's messageId).
func NewMapper(messageID string) *Mapper {
	return &Mapper{
		messageID:    messageID,
		lastActivity: map[string]planStepValue{},
	}
}

// Map translates one Gantry event into zero-or-more UI Message Stream
// chunks. It emits a "start" chunk lazily before the first translated
// event.
func (m *Mapper) Map(ev gantry.Event) []Chunk {
	if ev.ParentRunID != "" || ev.ParentToolCallID != "" {
		return nil
	}

	out := m.startFrame()

	if ev.Dropped > 0 {
		out = append(out, newDataChunk("gantry-events-dropped", "", eventsDroppedValue{Count: ev.Dropped}))
	}
	out = append(out, m.usageDelta(ev)...)
	out = append(out, m.stepBoundary(ev)...)

	switch ev.Type {
	case gantry.EventTextDelta:
		out = append(out, m.closeReasoning()...)
		if m.openText == "" {
			m.blockSeq++
			m.openText = fmt.Sprintf("%s:text:%d", m.messageID, m.blockSeq)
			out = append(out, newTextStart(m.openText))
		}
		out = append(out, newTextDelta(m.openText, ev.TextDelta))

	case gantry.EventReasoningDelta:
		out = append(out, m.closeText()...)
		if m.openReasoning == "" {
			m.blockSeq++
			m.openReasoning = fmt.Sprintf("%s:reasoning:%d", m.messageID, m.blockSeq)
			out = append(out, newReasoningStart(m.openReasoning))
		}
		out = append(out, newReasoningDelta(m.openReasoning, ev.ReasoningDelta))

	case gantry.EventRaw:
		out = append(out, newDataChunk("gantry-raw", "", rawValue{Event: ev.RawFrame, Source: ev.RawSource}))

	case gantry.EventPlanStepChanged:
		if ev.PlanStep != nil {
			out = append(out, m.activityChange(*ev.PlanStep)...)
		}

	case gantry.EventToolCall:
		out = append(out, m.closeText()...)
		out = append(out, m.closeReasoning()...)
		if tc := ev.ToolCall; tc != nil {
			out = append(out,
				newToolInputStart(tc.ID, tc.Name),
				newToolInputAvailable(tc.ID, tc.Name, tc.Input),
			)
		}

	case gantry.EventToolResult:
		out = append(out, m.closeText()...)
		out = append(out, m.closeReasoning()...)
		if tr := ev.ToolResult; tr != nil {
			if tr.IsError {
				out = append(out, newToolOutputError(tr.CallID, tr.Content))
			} else {
				out = append(out, newToolOutputAvailable(tr.CallID, tr.Content))
			}
		}

	case gantry.EventDone:
		out = append(out, m.closeText()...)
		out = append(out, m.closeReasoning()...)
		out = append(out, m.closeStep()...)
		out = append(out, newFinish())
	}

	return out
}

// startFrame returns a "start" chunk the first time it is called, marking
// the message started; later calls return nil. Map uses it lazily, and
// Sink uses it directly so an "error" chunk emitted before any Gantry
// event is still preceded by "start".
func (m *Mapper) startFrame() []Chunk {
	if m.started {
		return nil
	}
	m.started = true
	return []Chunk{newStart(m.messageID)}
}

// stepBoundary translates a Gantry phase transition into "start-step" /
// "finish-step" chunks. One AI SDK "step" corresponds to one Gantry loop
// iteration: PhaseAssembleContext is the first loop phase in each
// iteration (so its phase_start opens the step) and PhaseObserve is the
// last (so its phase_end closes it). PhaseStart/PhaseEnd -- the
// once-per-run brackets outside the loop -- never open or close a step;
// the protocol's own "start"/"finish" chunks already bracket the whole
// message.
func (m *Mapper) stepBoundary(ev gantry.Event) []Chunk {
	switch {
	case ev.Type == gantry.EventPhaseStart && ev.Phase == gantry.PhaseAssembleContext:
		if m.stepOpen {
			return nil // defensive: a step was already open, shouldn't happen
		}
		m.stepOpen = true
		return []Chunk{newStartStep()}
	case ev.Type == gantry.EventPhaseEnd && ev.Phase == gantry.PhaseObserve:
		// Close any still-open text/reasoning block before finish-step,
		// mirroring EventDone's order in Map -- otherwise a client could
		// see finish-step precede the text-end/reasoning-end that belongs
		// to the step it just closed. Not reachable through today's stock
		// gantry loop (EventToolCall already closes both before
		// PhaseObserve when there are tool calls, and the loop exits
		// before PhaseObserve when there aren't), but nothing enforces
		// that invariant, so defend here too.
		var out []Chunk
		out = append(out, m.closeText()...)
		out = append(out, m.closeReasoning()...)
		out = append(out, m.closeStep()...)
		return out
	}
	return nil
}

// closeStep emits "finish-step" if a step is open, and clears the flag.
// Returns nil if no step is open. EventDone also calls this, as a safety
// net for a run that ends mid-iteration without reaching PhaseObserve
// (e.g. an error, or a handoff that exits the loop early).
func (m *Mapper) closeStep() []Chunk {
	if !m.stepOpen {
		return nil
	}
	m.stepOpen = false
	return []Chunk{newFinishStep()}
}

// closeText emits "text-end" for the open text block, if any, and clears
// the open-text state. Returns nil if no block is open.
func (m *Mapper) closeText() []Chunk {
	if m.openText == "" {
		return nil
	}
	id := m.openText
	m.openText = ""
	return []Chunk{newTextEnd(id)}
}

// closeReasoning emits "reasoning-end" for the open reasoning block, if
// any, and clears the open-reasoning state. Mirrors closeText.
func (m *Mapper) closeReasoning() []Chunk {
	if m.openReasoning == "" {
		return nil
	}
	id := m.openReasoning
	m.openReasoning = ""
	return []Chunk{newReasoningEnd(id)}
}

// usageDelta returns a data-gantry-usage chunk if ev carries a Usage
// snapshot that differs from the last one emitted, updating the tracked
// value as a side effect; otherwise nil. Mirrors agui's usageDelta -- most
// phase_end/done events carry an unchanged Usage, so this keeps the wire
// from being spammed with a usage chunk on every one of them.
func (m *Mapper) usageDelta(ev gantry.Event) []Chunk {
	if ev.Usage == nil {
		return nil
	}
	if m.haveUsage && m.lastUsage == *ev.Usage {
		return nil
	}
	m.lastUsage = *ev.Usage
	m.haveUsage = true
	return []Chunk{newDataChunk("gantry-usage", "", usageValue{
		InputTokens:  ev.Usage.InputTokens,
		OutputTokens: ev.Usage.OutputTokens,
		Tokens:       ev.Usage.InputTokens + ev.Usage.OutputTokens,
		CostUSD:      ev.Usage.Cost,
	})}
}

// activityChange translates a changed gantry.PlanStep into a
// data-gantry-plan-step chunk, keyed by step.ID so a client tracking data
// parts by id can update the same logical part in place. Unlike agui's
// ACTIVITY_SNAPSHOT/ACTIVITY_DELTA split, data chunks have no JSON-Patch
// concept, so this always resends the full current value. Returns nil if
// the step's tracked fields haven't actually changed since the last report.
func (m *Mapper) activityChange(step gantry.PlanStep) []Chunk {
	cur := planStepValue{ID: step.ID, Description: step.Description, Status: string(step.Status), Output: step.Output}
	if last, ok := m.lastActivity[step.ID]; ok && last == cur {
		return nil
	}
	m.lastActivity[step.ID] = cur
	return []Chunk{newDataChunk("gantry-plan-step", step.ID, cur)}
}
