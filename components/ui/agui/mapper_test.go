package agui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestMapperLazyRunStartedThenStep(t *testing.T) {
	m := NewMapper("t1", "r1")
	got := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart, RunID: "run-1"})
	want := []Event{
		newRunStarted("t1", "r1"),
		newStepStarted("start").withIdentity(identity{RunID: "run-1"}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// RunStarted is emitted only once.
	got2 := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseStart, RunID: "run-1"})
	want2 := []Event{newStepFinished("start").withIdentity(identity{RunID: "run-1"})}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got  %#v\nwant %#v", got2, want2)
	}
}

func TestMapperTextMessageLifecycle(t *testing.T) {
	m := NewMapper("t1", "r1")
	id := identity{RunID: "run-1"}
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseLLMCall, RunID: "run-1"}) // RunStarted + StepStarted
	d1 := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "He", RunID: "run-1"})
	d2 := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "llo", RunID: "run-1"})
	end := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall, RunID: "run-1"})

	wantD1 := []Event{
		newTextMessageStart("run-1:msg:1").withIdentity(id),
		newTextMessageContent("run-1:msg:1", "He").withIdentity(id),
	}
	if !reflect.DeepEqual(d1, wantD1) {
		t.Fatalf("d1 got %#v\nwant %#v", d1, wantD1)
	}
	wantD2 := []Event{newTextMessageContent("run-1:msg:1", "llo").withIdentity(id)}
	if !reflect.DeepEqual(d2, wantD2) {
		t.Fatalf("d2 got %#v\nwant %#v", d2, wantD2)
	}
	// phase_end closes the open text message BEFORE the StepFinished.
	wantEnd := []Event{
		newTextMessageEnd("run-1:msg:1").withIdentity(id),
		newStepFinished("llm_call").withIdentity(id),
	}
	if !reflect.DeepEqual(end, wantEnd) {
		t.Fatalf("end got %#v\nwant %#v", end, wantEnd)
	}
}

func TestMapperToolCallClosesOpenText(t *testing.T) {
	m := NewMapper("t1", "r1")
	id := identity{RunID: "run-1"}
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "run-1"}) // RunStarted + start + content
	tc := &gantry.ToolCall{ID: "c1", Name: "search", Input: json.RawMessage(`{"q":"x"}`)}
	got := m.Map(gantry.Event{Type: gantry.EventToolCall, ToolCall: tc, RunID: "run-1"})
	want := []Event{
		newTextMessageEnd("run-1:msg:1").withIdentity(id),
		newToolCallStart("c1", "search").withIdentity(id),
		newToolCallArgs("c1", `{"q":"x"}`).withIdentity(id),
		newToolCallEnd("c1").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolResultAndDone(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true // skip lazy RunStarted for a focused assertion
	id := identity{RunID: "run-1"}
	tr := &gantry.ToolResult{CallID: "c1", Content: "ok"}
	gotRes := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: tr, RunID: "run-1"})
	wantRes := []Event{newToolCallResult("run-1:toolmsg:c1", "c1", "ok").withIdentity(id)}
	if !reflect.DeepEqual(gotRes, wantRes) {
		t.Fatalf("res got %#v\nwant %#v", gotRes, wantRes)
	}
	gotDone := m.Map(gantry.Event{Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls, RunID: "run-1"})
	wantDone := []Event{newRunFinished("t1", "r1")}
	if !reflect.DeepEqual(gotDone, wantDone) {
		t.Fatalf("done got %#v\nwant %#v", gotDone, wantDone)
	}
}

func TestMapperSecondTextMessageIncrementsID(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true // focus on message-id sequencing

	// First message opens at run-1:msg:1, then a phase boundary closes it.
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "one", RunID: "run-1"})
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall, RunID: "run-1"})

	// A later text delta must open a FRESH message at run-1:msg:2.
	got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "two", RunID: "run-1"})
	want := []Event{
		newTextMessageStart("run-1:msg:2").withIdentity(identity{RunID: "run-1"}),
		newTextMessageContent("run-1:msg:2", "two").withIdentity(identity{RunID: "run-1"}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperDoneClosesOpenText(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "bye", RunID: "run-1"}) // opens run-1:msg:1
	got := m.Map(gantry.Event{Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls, RunID: "run-1"})
	want := []Event{
		newTextMessageEnd("run-1:msg:1").withIdentity(id),
		newRunFinished("t1", "r1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// --- New: nested sub-agent identity/scoping coverage ---

func TestMapperNestedDoneEmitsSubagentDoneNotRunFinished(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	got := m.Map(gantry.Event{
		Type:             gantry.EventDone,
		DoneReason:       gantry.DoneNoToolCalls,
		RunID:            "run-child",
		Agent:            "investigation",
		ParentRunID:      "run-parent",
		ParentToolCallID: "call-1",
	})
	want := []Event{newSubagentDone(identity{
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperTopLevelDoneStillEmitsRunFinished(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	got := m.Map(gantry.Event{
		Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls,
		RunID: "run-parent", Agent: "orchestrator",
	})
	want := []Event{newRunFinished("t1", "r1")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperInterleavedRunsBracketTextIndependently(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	// Parent opens a text message...
	pStart := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "parent-1", RunID: "run-parent", Agent: "orchestrator"})
	if len(pStart) != 2 { // TEXT_MESSAGE_START + CONTENT
		t.Fatalf("parent start: got %d events, want 2: %#v", len(pStart), pStart)
	}
	// ...then, before the parent's message closes, the child opens its OWN.
	cStart := m.Map(gantry.Event{
		Type: gantry.EventTextDelta, TextDelta: "child-1",
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	if len(cStart) != 2 {
		t.Fatalf("child start: got %d events, want 2: %#v", len(cStart), cStart)
	}
	childMsgID := cStart[0].(TextMessageStart).MessageID
	parentMsgID := pStart[0].(TextMessageStart).MessageID
	if childMsgID == parentMsgID {
		t.Fatalf("child and parent text messages share messageId %q, want distinct ids", childMsgID)
	}

	// More parent content must append to the PARENT's still-open message,
	// not the child's.
	pMore := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "parent-2", RunID: "run-parent", Agent: "orchestrator"})
	if len(pMore) != 1 {
		t.Fatalf("parent continuation: got %d events, want 1 (no spurious START): %#v", len(pMore), pMore)
	}
	if got := pMore[0].(TextMessageContent).MessageID; got != parentMsgID {
		t.Errorf("parent continuation messageId = %q, want %q", got, parentMsgID)
	}

	// Closing the child does not touch the parent's still-open message.
	cDone := m.Map(gantry.Event{
		Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls,
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	var sawChildTextEnd, sawSubagentDone bool
	for _, ev := range cDone {
		if end, ok := ev.(TextMessageEnd); ok && end.MessageID == childMsgID {
			sawChildTextEnd = true
		}
		if c, ok := ev.(Custom); ok && c.Name == subagentDoneName {
			sawSubagentDone = true
		}
	}
	if !sawChildTextEnd {
		t.Errorf("child done: expected TEXT_MESSAGE_END for %q, got %#v", childMsgID, cDone)
	}
	if !sawSubagentDone {
		t.Errorf("child done: expected a gantry.subagent_done CUSTOM event, got %#v", cDone)
	}

	// The parent's message is STILL open — closing it independently must
	// still work and reference the parent's own messageId.
	pDone := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall, RunID: "run-parent", Agent: "orchestrator"})
	if len(pDone) != 2 { // TEXT_MESSAGE_END + STEP_FINISHED
		t.Fatalf("parent phase end: got %d events, want 2: %#v", len(pDone), pDone)
	}
	if end, ok := pDone[0].(TextMessageEnd); !ok || end.MessageID != parentMsgID {
		t.Errorf("parent phase end TEXT_MESSAGE_END messageId = %#v, want %q", pDone[0], parentMsgID)
	}
}
