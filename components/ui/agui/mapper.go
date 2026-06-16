package agui

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// Mapper translates Gantry's whole-run Event stream into AG-UI protocol events.
// It is stateful and NOT safe for concurrent use: it tracks whether RUN_STARTED
// has been emitted and whether a text message is currently open, so it can
// bracket text correctly and close an open message before any other event.
// Use one Mapper per run.
type Mapper struct {
	threadID string
	runID    string
	started  bool   // RUN_STARTED emitted?
	openMsg  string // non-empty messageId while a text message is open
	msgSeq   int    // monotonic counter for synthesized messageIds
}

// NewMapper returns a Mapper for a single run identified by threadID/runID.
func NewMapper(threadID, runID string) *Mapper {
	return &Mapper{threadID: threadID, runID: runID}
}

// Map translates one Gantry event into zero-or-more AG-UI events. It emits
// RUN_STARTED lazily before the first translated event, and closes any open
// text message (with TEXT_MESSAGE_END) before emitting a non-text event.
// RUN_ERROR is intentionally not produced here — a Go error is not part of the
// Event stream; the Sink emits it from RunStream's returned error.
func (m *Mapper) Map(ev gantry.Event) []Event {
	out := m.startFrame()

	switch ev.Type {
	case gantry.EventTextDelta:
		if m.openMsg == "" {
			m.msgSeq++
			m.openMsg = fmt.Sprintf("%s:msg:%d", m.runID, m.msgSeq)
			out = append(out, newTextMessageStart(m.openMsg))
		}
		out = append(out, newTextMessageContent(m.openMsg, ev.TextDelta))

	case gantry.EventToolCall:
		out = append(out, m.closeText()...)
		if tc := ev.ToolCall; tc != nil {
			out = append(out,
				newToolCallStart(tc.ID, tc.Name),
				newToolCallArgs(tc.ID, string(tc.Input)),
				newToolCallEnd(tc.ID),
			)
		}

	case gantry.EventToolResult:
		out = append(out, m.closeText()...)
		if tr := ev.ToolResult; tr != nil {
			msgID := fmt.Sprintf("%s:toolmsg:%s", m.runID, tr.CallID)
			out = append(out, newToolCallResult(msgID, tr.CallID, tr.Content))
		}

	case gantry.EventPhaseStart:
		out = append(out, m.closeText()...)
		out = append(out, newStepStarted(string(ev.Phase)))

	case gantry.EventPhaseEnd:
		out = append(out, m.closeText()...)
		out = append(out, newStepFinished(string(ev.Phase)))

	case gantry.EventDone:
		out = append(out, m.closeText()...)
		out = append(out, newRunFinished(m.threadID, m.runID))
	}

	return out
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

// closeText emits TEXT_MESSAGE_END for an open text message, if any, and clears
// the open-message state. Returns nil when no message is open.
func (m *Mapper) closeText() []Event {
	if m.openMsg == "" {
		return nil
	}
	id := m.openMsg
	m.openMsg = ""
	return []Event{newTextMessageEnd(id)}
}
