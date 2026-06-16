package agui

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestMapperLazyRunStartedThenStep(t *testing.T) {
	m := NewMapper("t1", "r1")
	got := m.Map(harness.Event{Type: harness.EventPhaseStart, Phase: harness.PhaseStart})
	want := []Event{
		newRunStarted("t1", "r1"),
		newStepStarted("start"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// RunStarted is emitted only once.
	got2 := m.Map(harness.Event{Type: harness.EventPhaseEnd, Phase: harness.PhaseStart})
	want2 := []Event{newStepFinished("start")}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got  %#v\nwant %#v", got2, want2)
	}
}

func TestMapperTextMessageLifecycle(t *testing.T) {
	m := NewMapper("t1", "r1")
	_ = m.Map(harness.Event{Type: harness.EventPhaseStart, Phase: harness.PhaseLLMCall}) // RunStarted + StepStarted
	d1 := m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "He"})
	d2 := m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "llo"})
	end := m.Map(harness.Event{Type: harness.EventPhaseEnd, Phase: harness.PhaseLLMCall})

	wantD1 := []Event{
		newTextMessageStart("r1:msg:1"),
		newTextMessageContent("r1:msg:1", "He"),
	}
	if !reflect.DeepEqual(d1, wantD1) {
		t.Fatalf("d1 got %#v\nwant %#v", d1, wantD1)
	}
	wantD2 := []Event{newTextMessageContent("r1:msg:1", "llo")}
	if !reflect.DeepEqual(d2, wantD2) {
		t.Fatalf("d2 got %#v\nwant %#v", d2, wantD2)
	}
	// phase_end closes the open text message BEFORE the StepFinished.
	wantEnd := []Event{
		newTextMessageEnd("r1:msg:1"),
		newStepFinished("llm_call"),
	}
	if !reflect.DeepEqual(end, wantEnd) {
		t.Fatalf("end got %#v\nwant %#v", end, wantEnd)
	}
}

func TestMapperToolCallClosesOpenText(t *testing.T) {
	m := NewMapper("t1", "r1")
	_ = m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "hi"}) // RunStarted + start + content
	tc := &harness.ToolCall{ID: "c1", Name: "search", Input: json.RawMessage(`{"q":"x"}`)}
	got := m.Map(harness.Event{Type: harness.EventToolCall, ToolCall: tc})
	want := []Event{
		newTextMessageEnd("r1:msg:1"),
		newToolCallStart("c1", "search"),
		newToolCallArgs("c1", `{"q":"x"}`),
		newToolCallEnd("c1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolResultAndDone(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true // skip lazy RunStarted for a focused assertion
	tr := &harness.ToolResult{CallID: "c1", Content: "ok"}
	gotRes := m.Map(harness.Event{Type: harness.EventToolResult, ToolResult: tr})
	wantRes := []Event{newToolCallResult("r1:toolmsg:c1", "c1", "ok")}
	if !reflect.DeepEqual(gotRes, wantRes) {
		t.Fatalf("res got %#v\nwant %#v", gotRes, wantRes)
	}
	gotDone := m.Map(harness.Event{Type: harness.EventDone, DoneReason: harness.DoneNoToolCalls})
	wantDone := []Event{newRunFinished("t1", "r1")}
	if !reflect.DeepEqual(gotDone, wantDone) {
		t.Fatalf("done got %#v\nwant %#v", gotDone, wantDone)
	}
}

func TestMapperSecondTextMessageIncrementsID(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true // focus on message-id sequencing

	// First message opens at :msg:1, then a phase boundary closes it.
	_ = m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "one"})
	_ = m.Map(harness.Event{Type: harness.EventPhaseEnd, Phase: harness.PhaseLLMCall})

	// A later text delta must open a FRESH message at :msg:2.
	got := m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "two"})
	want := []Event{
		newTextMessageStart("r1:msg:2"),
		newTextMessageContent("r1:msg:2", "two"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperDoneClosesOpenText(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	_ = m.Map(harness.Event{Type: harness.EventTextDelta, TextDelta: "bye"}) // opens r1:msg:1
	got := m.Map(harness.Event{Type: harness.EventDone, DoneReason: harness.DoneNoToolCalls})
	want := []Event{
		newTextMessageEnd("r1:msg:1"),
		newRunFinished("t1", "r1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}
