package vercelai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestMapperLazyStartThenText(t *testing.T) {
	m := NewMapper("m1")
	got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "He"})
	want := []Chunk{
		newStart("m1"),
		newTextStart("m1:text:1"),
		newTextDelta("m1:text:1", "He"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// start is emitted only once.
	got2 := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "llo"})
	want2 := []Chunk{newTextDelta("m1:text:1", "llo")}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("got  %#v\nwant %#v", got2, want2)
	}
}

func TestMapperReasoningClosesOpenText(t *testing.T) {
	m := NewMapper("m1")
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"}) // start + text-start + text-delta
	got := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "hmm"})
	want := []Chunk{
		newTextEnd("m1:text:1"),
		newReasoningStart("m1:reasoning:2"),
		newReasoningDelta("m1:reasoning:2", "hmm"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperTextClosesOpenReasoning(t *testing.T) {
	m := NewMapper("m1")
	_ = m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "hmm"}) // start + reasoning-start + delta
	got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"})
	want := []Chunk{
		newReasoningEnd("m1:reasoning:1"),
		newTextStart("m1:text:2"),
		newTextDelta("m1:text:2", "hi"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolCallClosesOpenText(t *testing.T) {
	m := NewMapper("m1")
	m.started = true // skip the lazy "start" chunk for a focused assertion
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"})
	tc := &gantry.ToolCall{ID: "c1", Name: "search", Input: json.RawMessage(`{"q":"x"}`)}
	got := m.Map(gantry.Event{Type: gantry.EventToolCall, ToolCall: tc})
	want := []Chunk{
		newTextEnd("m1:text:1"),
		newToolInputStart("c1", "search"),
		newToolInputAvailable("c1", "search", json.RawMessage(`{"q":"x"}`)),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolResult(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "ok"}
	got := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: tr})
	want := []Chunk{newToolOutputAvailable("c1", "ok")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}

	trErr := &gantry.ToolResult{CallID: "c2", Content: "boom", IsError: true}
	gotErr := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: trErr})
	wantErr := []Chunk{newToolOutputError("c2", "boom")}
	if !reflect.DeepEqual(gotErr, wantErr) {
		t.Fatalf("got  %#v\nwant %#v", gotErr, wantErr)
	}
}

func TestMapperToolResultLiveEmitsResult(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "ok"}
	got := m.Map(gantry.Event{Type: gantry.EventToolResultLive, ToolResult: tr})
	want := []Chunk{newToolOutputAvailable("c1", "ok")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolResultLiveErrorEmitsErrorOutput(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "boom", IsError: true}
	got := m.Map(gantry.Event{Type: gantry.EventToolResultLive, ToolResult: tr})
	want := []Chunk{newToolOutputError("c1", "boom")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

// TestMapperToolResultLiveSuppressesLaterBatchedResult proves a call that
// already got a real-time result via EventToolResultLive does not also
// surface a second, redundant tool-output chunk when the core run loop's
// later batched EventToolResult (see run_stream.go's emitPhaseEffects)
// reports the same call.
func TestMapperToolResultLiveSuppressesLaterBatchedResult(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "ok"}
	live := m.Map(gantry.Event{Type: gantry.EventToolResultLive, ToolResult: tr})
	if len(live) != 1 {
		t.Fatalf("live got %#v, want exactly one chunk", live)
	}
	got := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: tr})
	if got != nil {
		t.Fatalf("batched result after live got %#v, want nil (already delivered)", got)
	}
}

// TestMapperToolResultWithoutLiveStillEmits is the non-regression
// counterpart above: a call that never got an EventToolResultLive still
// gets its result from the batched EventToolResult exactly as before this
// feature existed.
func TestMapperToolResultWithoutLiveStillEmits(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "ok"}
	got := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: tr})
	want := []Chunk{newToolOutputAvailable("c1", "ok")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperStepBoundary(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	got := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseAssembleContext})
	want := []Chunk{newStartStep()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("start-step: got %#v, want %#v", got, want)
	}

	// Non-boundary phases produce nothing.
	if got := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseLLMCall}); got != nil {
		t.Fatalf("llm_call phase_start: got %#v, want nil", got)
	}
	if got := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall}); got != nil {
		t.Fatalf("llm_call phase_end: got %#v, want nil", got)
	}

	gotEnd := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseObserve})
	wantEnd := []Chunk{newFinishStep()}
	if !reflect.DeepEqual(gotEnd, wantEnd) {
		t.Fatalf("finish-step: got %#v, want %#v", gotEnd, wantEnd)
	}
}

func TestMapperDoneClosesOpenTextAndStep(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseAssembleContext}) // start-step
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"})                     // text-start + delta
	got := m.Map(gantry.Event{Type: gantry.EventDone})
	want := []Chunk{
		newTextEnd("m1:text:1"),
		newFinishStep(),
		newFinish(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperUsageDedup(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	u := gantry.Usage{InputTokens: 10, OutputTokens: 5, Cost: 0.001}
	// No step is open (no PhaseAssembleContext seen yet), so PhaseObserve's
	// phase_end contributes only the usage chunk -- closeStep() is a no-op
	// when stepOpen is false.
	got := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseObserve, Usage: &u})
	want := []Chunk{
		newDataChunk("gantry-usage", "", usageValue{InputTokens: 10, OutputTokens: 5, Tokens: 15, CostUSD: 0.001}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// Same usage value again -> no repeat data-gantry-usage chunk.
	got2 := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseObserve, Usage: &u})
	if got2 != nil {
		t.Fatalf("got  %#v\nwant nil (usage unchanged)", got2)
	}
}

func TestMapperStepBoundaryClosesOpenTextFirst(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseAssembleContext}) // start-step
	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"})                     // text-start + delta

	// PhaseObserve's phase_end fires directly, without an intervening
	// EventToolCall/EventDone to close the open text block first. The
	// resulting chunk order must still be text-end before finish-step.
	got := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseObserve})
	want := []Chunk{
		newTextEnd("m1:text:1"),
		newFinishStep(),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperPlanStepActivity(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	step := gantry.PlanStep{ID: "s1", Description: "design", Status: "active"}
	got := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, PlanStep: &step})
	want := []Chunk{newDataChunk("gantry-plan-step", "s1", planStepValue{ID: "s1", Description: "design", Status: "active"})}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
	// No change -> no repeat chunk.
	if got2 := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, PlanStep: &step}); got2 != nil {
		t.Fatalf("got  %#v\nwant nil (no change)", got2)
	}
	// Status changes -> a fresh full snapshot (no JSON-Patch delta concept here).
	step2 := gantry.PlanStep{ID: "s1", Description: "design", Status: "done", Output: "done."}
	got3 := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, PlanStep: &step2})
	want3 := []Chunk{newDataChunk("gantry-plan-step", "s1", planStepValue{ID: "s1", Description: "design", Status: "done", Output: "done."})}
	if !reflect.DeepEqual(got3, want3) {
		t.Fatalf("got  %#v\nwant %#v", got3, want3)
	}
}

func TestMapperRawAndDropped(t *testing.T) {
	m := NewMapper("m1")
	m.started = true
	got := m.Map(gantry.Event{Type: gantry.EventRaw, RawFrame: json.RawMessage(`{"a":1}`), RawSource: "anthropic", Dropped: 2})
	want := []Chunk{
		newDataChunk("gantry-events-dropped", "", eventsDroppedValue{Count: 2}),
		newDataChunk("gantry-raw", "", rawValue{Event: json.RawMessage(`{"a":1}`), Source: "anthropic"}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperDropsNestedSubAgentEvents(t *testing.T) {
	m := NewMapper("m1")
	if got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", ParentRunID: "parent-run"}); got != nil {
		t.Fatalf("got  %#v\nwant nil (ParentRunID set)", got)
	}
	if got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", ParentToolCallID: "call-1"}); got != nil {
		t.Fatalf("got  %#v\nwant nil (ParentToolCallID set)", got)
	}
	// "start" is still not emitted, since every event so far was dropped.
	if m.started {
		t.Fatal("started = true, want false (no top-level event has been mapped yet)")
	}
}
