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
	wantRes := []Event{newToolCallResult("run-1:toolmsg:c1", "c1", "ok", false).withIdentity(id)}
	if !reflect.DeepEqual(gotRes, wantRes) {
		t.Fatalf("res got %#v\nwant %#v", gotRes, wantRes)
	}
	gotDone := m.Map(gantry.Event{Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls, RunID: "run-1"})
	wantDone := []Event{newRunFinished("t1", "r1")}
	if !reflect.DeepEqual(gotDone, wantDone) {
		t.Fatalf("done got %#v\nwant %#v", gotDone, wantDone)
	}
}

func TestMapperToolResultError(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	tr := &gantry.ToolResult{CallID: "c1", Content: "boom", IsError: true}
	got := m.Map(gantry.Event{Type: gantry.EventToolResult, ToolResult: tr, RunID: "run-1"})
	want := []Event{newToolCallResult("run-1:toolmsg:c1", "c1", "boom", true).withIdentity(identity{RunID: "run-1"})}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperUsageEmittedOnlyWhenChanged(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	u1 := gantry.Usage{InputTokens: 10, OutputTokens: 5, Cost: 0.001}
	got := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseStart, RunID: "run-1", Usage: &u1})
	want := []Event{
		newUsage(10, 5, 0.001, id),
		newStepFinished("start").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("first: got  %#v\nwant %#v", got, want)
	}

	// Same usage value again (a phase that made no LLM call): no usage event.
	got = m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseObserve, RunID: "run-1", Usage: &u1})
	want = []Event{newStepFinished("observe").withIdentity(id)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged: got  %#v\nwant %#v", got, want)
	}

	// Usage increased: a fresh USAGE event.
	u2 := gantry.Usage{InputTokens: 20, OutputTokens: 12, Cost: 0.003}
	got = m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall, RunID: "run-1", Usage: &u2})
	want = []Event{
		newUsage(20, 12, 0.003, id),
		newStepFinished("llm_call").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("changed: got  %#v\nwant %#v", got, want)
	}
}

func TestMapperEventsDroppedPrecedesTranslatedEvent(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	got := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseLLMCall, RunID: "run-1", Dropped: 4})
	want := []Event{
		newEventsDropped(4, identity{RunID: "run-1"}),
		newStepStarted("llm_call").withIdentity(identity{RunID: "run-1"}),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
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

func TestMapperNestedDoneClassifiesByDoneReason(t *testing.T) {
	cases := []struct {
		name       string
		reason     gantry.DoneReason
		wantFinish bool // true: SUBAGENT_FINISHED, false: SUBAGENT_ERROR
		suspended  bool
	}{
		{"no_tool_calls", gantry.DoneNoToolCalls, true, false},
		{"max_iterations", gantry.DoneMaxIterations, true, false},
		{"budget_exceeded", gantry.DoneBudgetExceeded, true, false},
		{"client_tool_call_suspends", gantry.DoneClientToolCall, true, true},
		{"error", gantry.DoneError, false, false},
		{"guardrail_blocked", gantry.DoneGuardrailBlocked, false, false},
		{"human_aborted", gantry.DoneHumanAborted, false, false},
		{"tool_policy_aborted", gantry.DoneToolPolicyAborted, false, false},
		{"handoff", gantry.DoneHandoff, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewMapper("t1", "r1")
			m.started = true
			m.subagentStarted = map[string]bool{"run-child": true} // isolate from lazy-start
			id := identity{
				RunID: "run-child", Agent: "investigation",
				ParentRunID: "run-parent", ParentToolCallID: "call-1", SubagentRunID: "run-child",
			}
			got := m.Map(gantry.Event{
				Type: gantry.EventDone, DoneReason: tc.reason,
				RunID: "run-child", Agent: "investigation",
				ParentRunID: "run-parent", ParentToolCallID: "call-1",
				FinalOutput: "42 facts",
			})
			var want []Event
			if tc.wantFinish {
				want = []Event{newSubagentFinished("run-child", "42 facts", tc.suspended).withIdentity(id)}
			} else {
				want = []Event{newSubagentError("run-child", "sub-agent run terminated: "+string(tc.reason)).withIdentity(id)}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s: got  %#v\nwant %#v", tc.name, got, want)
			}
		})
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
	m.subagentStarted = map[string]bool{"run-child": true}

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
	var sawChildTextEnd, sawSubagentFinished bool
	for _, ev := range cDone {
		if end, ok := ev.(TextMessageEnd); ok && end.MessageID == childMsgID {
			sawChildTextEnd = true
		}
		if _, ok := ev.(SubagentFinished); ok {
			sawSubagentFinished = true
		}
	}
	if !sawChildTextEnd {
		t.Errorf("child done: expected TEXT_MESSAGE_END for %q, got %#v", childMsgID, cDone)
	}
	if !sawSubagentFinished {
		t.Errorf("child done: expected a SUBAGENT_FINISHED event, got %#v", cDone)
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

// TestMapperInterleavedRunsBracketReasoningIndependently mirrors
// TestMapperInterleavedRunsBracketTextIndependently, for reasoning messages:
// openReasoning is keyed by gantry RunID exactly like openMsg, so the same
// per-run independence guarantee must hold for reasoning as it does for text.
func TestMapperInterleavedRunsBracketReasoningIndependently(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	m.subagentStarted = map[string]bool{"run-child": true}

	// Parent opens a reasoning message...
	pStart := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "parent-thinking", RunID: "run-parent", Agent: "orchestrator"})
	if len(pStart) != 3 { // REASONING_START + REASONING_MESSAGE_START + CONTENT
		t.Fatalf("parent start: got %d events, want 3: %#v", len(pStart), pStart)
	}
	// ...then, before the parent's message closes, the child opens its OWN.
	cStart := m.Map(gantry.Event{
		Type: gantry.EventReasoningDelta, ReasoningDelta: "child-thinking",
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	if len(cStart) != 3 {
		t.Fatalf("child start: got %d events, want 3: %#v", len(cStart), cStart)
	}
	childMsgID := cStart[0].(ReasoningStart).MessageID
	parentMsgID := pStart[0].(ReasoningStart).MessageID
	if childMsgID == parentMsgID {
		t.Fatalf("child and parent reasoning messages share messageId %q, want distinct ids", childMsgID)
	}

	// More parent content must append to the PARENT's still-open message,
	// not the child's.
	pMore := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "parent-more", RunID: "run-parent", Agent: "orchestrator"})
	if len(pMore) != 1 {
		t.Fatalf("parent continuation: got %d events, want 1 (no spurious START): %#v", len(pMore), pMore)
	}
	if got := pMore[0].(ReasoningMessageContent).MessageID; got != parentMsgID {
		t.Errorf("parent continuation messageId = %q, want %q", got, parentMsgID)
	}

	// Closing the child does not touch the parent's still-open reasoning message.
	cDone := m.Map(gantry.Event{
		Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls,
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	var sawChildReasoningEnd bool
	for _, ev := range cDone {
		if end, ok := ev.(ReasoningEnd); ok && end.MessageID == childMsgID {
			sawChildReasoningEnd = true
		}
	}
	if !sawChildReasoningEnd {
		t.Errorf("child done: expected REASONING_END for %q, got %#v", childMsgID, cDone)
	}

	// The parent's reasoning message is STILL open — closing it independently
	// must still work and reference the parent's own messageId.
	pDone := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseLLMCall, RunID: "run-parent", Agent: "orchestrator"})
	var sawParentReasoningEnd bool
	for _, ev := range pDone {
		if end, ok := ev.(ReasoningEnd); ok && end.MessageID == parentMsgID {
			sawParentReasoningEnd = true
		}
	}
	if !sawParentReasoningEnd {
		t.Errorf("parent phase end: expected REASONING_END for %q, got %#v", parentMsgID, pDone)
	}
}

func TestMapperReasoningMessageLifecycle(t *testing.T) {
	m := NewMapper("t1", "r1")
	id := identity{RunID: "run-1"}
	_ = m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseLLMCall, RunID: "run-1"}) // RunStarted + StepStarted

	d1 := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "Let me ", RunID: "run-1"})
	wantD1 := []Event{
		newReasoningStart("run-1:reasoning:1").withIdentity(id),
		newReasoningMessageStart("run-1:reasoning:1").withIdentity(id),
		newReasoningMessageContent("run-1:reasoning:1", "Let me ").withIdentity(id),
	}
	if !reflect.DeepEqual(d1, wantD1) {
		t.Fatalf("d1 got %#v\nwant %#v", d1, wantD1)
	}

	d2 := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "think", RunID: "run-1"})
	wantD2 := []Event{newReasoningMessageContent("run-1:reasoning:1", "think").withIdentity(id)}
	if !reflect.DeepEqual(d2, wantD2) {
		t.Fatalf("d2 got %#v\nwant %#v", d2, wantD2)
	}

	// A text delta closes the open reasoning message before opening its own.
	textStart := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "answer", RunID: "run-1"})
	wantTextStart := []Event{
		newReasoningMessageEnd("run-1:reasoning:1").withIdentity(id),
		newReasoningEnd("run-1:reasoning:1").withIdentity(id),
		newTextMessageStart("run-1:msg:2").withIdentity(id),
		newTextMessageContent("run-1:msg:2", "answer").withIdentity(id),
	}
	if !reflect.DeepEqual(textStart, wantTextStart) {
		t.Fatalf("textStart got %#v\nwant %#v", textStart, wantTextStart)
	}
}

func TestMapperTextDeltaClosesOpenReasoning(t *testing.T) {
	// Covered by TestMapperReasoningMessageLifecycle's third assertion; this
	// test covers the symmetric direction: a reasoning delta closes open text.
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "run-1"}) // opens run-1:msg:1
	got := m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "wait", RunID: "run-1"})
	want := []Event{
		newTextMessageEnd("run-1:msg:1").withIdentity(id),
		newReasoningStart("run-1:reasoning:2").withIdentity(id),
		newReasoningMessageStart("run-1:reasoning:2").withIdentity(id),
		newReasoningMessageContent("run-1:reasoning:2", "wait").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperToolCallClosesOpenReasoning(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	_ = m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "hmm", RunID: "run-1"})
	tc := &gantry.ToolCall{ID: "c1", Name: "search", Input: json.RawMessage(`{}`)}
	got := m.Map(gantry.Event{Type: gantry.EventToolCall, ToolCall: tc, RunID: "run-1"})
	want := []Event{
		newReasoningMessageEnd("run-1:reasoning:1").withIdentity(id),
		newReasoningEnd("run-1:reasoning:1").withIdentity(id),
		newToolCallStart("c1", "search").withIdentity(id),
		newToolCallArgs("c1", `{}`).withIdentity(id),
		newToolCallEnd("c1").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperDoneClosesOpenReasoning(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	_ = m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "bye", RunID: "run-1"})
	got := m.Map(gantry.Event{Type: gantry.EventDone, DoneReason: gantry.DoneNoToolCalls, RunID: "run-1"})
	want := []Event{
		newReasoningMessageEnd("run-1:reasoning:1").withIdentity(id),
		newReasoningEnd("run-1:reasoning:1").withIdentity(id),
		newRunFinished("t1", "r1"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperNestedStepNameIsNamespacedByRunID(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	m.subagentStarted = map[string]bool{"run-child": true}

	// Parent's own phase, unsuffixed -- exactly as before this fix.
	pGot := m.Map(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseToolExec, RunID: "run-parent", Agent: "orchestrator"})
	pStep, ok := pGot[0].(StepStarted)
	if !ok {
		t.Fatalf("parent event 0 = %#v, want StepStarted", pGot[0])
	}
	if pStep.StepName != "tool_exec" {
		t.Errorf("parent StepName = %q, want bare %q (top-level run, unsuffixed)", pStep.StepName, "tool_exec")
	}

	// A NESTED run's phase of the SAME name must NOT collide on the wire --
	// this is exactly the scenario that made @ag-ui/client's own protocol
	// verifier throw "Step already active", since it tracks STEP_STARTED/
	// STEP_FINISHED by stepName alone, globally, with no notion of "which run".
	cGot := m.Map(gantry.Event{
		Type: gantry.EventPhaseStart, Phase: gantry.PhaseToolExec,
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	cStep, ok := cGot[0].(StepStarted)
	if !ok {
		t.Fatalf("child event 0 = %#v, want StepStarted", cGot[0])
	}
	if cStep.StepName == "tool_exec" {
		t.Fatalf("child StepName = %q, want it namespaced by RunID (must differ from the parent's bare %q to avoid a wire collision)", cStep.StepName, "tool_exec")
	}
	if cStep.StepName != "tool_exec::run-child" {
		t.Errorf("child StepName = %q, want %q", cStep.StepName, "tool_exec::run-child")
	}

	// STEP_FINISHED must use the SAME namespaced name as its matching
	// STEP_STARTED, or a client would see an unmatched finish.
	cFin := m.Map(gantry.Event{
		Type: gantry.EventPhaseEnd, Phase: gantry.PhaseToolExec,
		RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	cFinStep, ok := cFin[0].(StepFinished)
	if !ok {
		t.Fatalf("child finish event 0 = %#v, want StepFinished", cFin[0])
	}
	if cFinStep.StepName != cStep.StepName {
		t.Errorf("child StepFinished.StepName = %q, want it to match the StepStarted name %q", cFinStep.StepName, cStep.StepName)
	}

	// The parent's own STEP_FINISHED for the SAME phase must still be bare --
	// unaffected by the child's namespacing.
	pFin := m.Map(gantry.Event{Type: gantry.EventPhaseEnd, Phase: gantry.PhaseToolExec, RunID: "run-parent", Agent: "orchestrator"})
	pFinStep, ok := pFin[0].(StepFinished)
	if !ok {
		t.Fatalf("parent finish event 0 = %#v, want StepFinished", pFin[0])
	}
	if pFinStep.StepName != "tool_exec" {
		t.Errorf("parent StepFinished.StepName = %q, want bare %q", pFinStep.StepName, "tool_exec")
	}
}

func TestMapperLazySubagentStarted(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1", SubagentRunID: "run-child",
	}

	got := m.Map(gantry.Event{
		Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart,
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1",
		ParentToolName: "researcher", ParentToolDescription: "Delegates fact-finding",
	})
	want := []Event{
		newSubagentStarted("run-child", "researcher", "Delegates fact-finding", "").withIdentity(id),
		newStepStarted("start::run-child").withIdentity(id),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}

	// A later event from the SAME RunID must not fire SUBAGENT_STARTED again.
	got2 := m.Map(gantry.Event{
		Type: gantry.EventPhaseEnd, Phase: gantry.PhaseStart,
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	want2 := []Event{newStepFinished("start::run-child").withIdentity(id)}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("second event: got  %#v\nwant %#v", got2, want2)
	}
}

func TestMapperLazySubagentStartedOmitsParentSubagentRunIDWhenParentIsTopLevel(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	got := m.Map(gantry.Event{
		Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart,
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	started, ok := got[0].(SubagentStarted)
	if !ok {
		t.Fatalf("event 0 = %#v, want SubagentStarted", got[0])
	}
	if started.ParentSubagentRunID != "" {
		t.Errorf("ParentSubagentRunID = %q, want empty (parent is top-level, never itself a subagent)", started.ParentSubagentRunID)
	}
}

func TestMapperLazySubagentStartedSetsParentSubagentRunIDWhenParentIsSubagent(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	// The parent ("run-child") is itself a nested sub-agent of "run-root".
	_ = m.Map(gantry.Event{
		Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart,
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-root", ParentToolCallID: "call-1",
	})

	// A grandchild nested under "run-child" must report run-child as its
	// parentSubagentRunId, since run-child is itself a subagent.
	got := m.Map(gantry.Event{
		Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart,
		RunID: "run-grandchild", Agent: "verification",
		ParentRunID: "run-child", ParentToolCallID: "call-2",
	})
	started, ok := got[0].(SubagentStarted)
	if !ok {
		t.Fatalf("event 0 = %#v, want SubagentStarted", got[0])
	}
	if started.ParentSubagentRunID != "run-child" {
		t.Errorf("ParentSubagentRunID = %q, want run-child (its parent is itself a subagent)", started.ParentSubagentRunID)
	}
}

func TestMapperOrdinaryNestedEventsCarrySubagentRunID(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	m.subagentStarted = map[string]bool{"run-child": true} // isolate from the lazy-start behavior itself

	got := m.Map(gantry.Event{
		Type: gantry.EventTextDelta, TextDelta: "hi",
		RunID: "run-child", ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	start, ok := got[0].(TextMessageStart)
	if !ok {
		t.Fatalf("event 0 = %#v, want TextMessageStart", got[0])
	}
	if start.SubagentRunID != "run-child" {
		t.Errorf("SubagentRunID = %q, want run-child", start.SubagentRunID)
	}
}

func TestMapperTopLevelEventsOmitSubagentRunID(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	got := m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "run-1"})
	start, ok := got[0].(TextMessageStart)
	if !ok {
		t.Fatalf("event 0 = %#v, want TextMessageStart", got[0])
	}
	if start.SubagentRunID != "" {
		t.Errorf("SubagentRunID = %q, want empty for a top-level run", start.SubagentRunID)
	}
}

// TestMapperLazySubagentStartedNotPhaseStartSpecific confirms the lazy
// SUBAGENT_STARTED emission fires off ANY first event for a not-yet-seen
// nested RunID -- not just EventPhaseStart, which is what every other lazy-
// start test above happens to use as its first event.
func TestMapperLazySubagentStartedNotPhaseStartSpecific(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	got := m.Map(gantry.Event{
		Type: gantry.EventTextDelta, TextDelta: "hi",
		RunID: "run-child", Agent: "investigation",
		ParentRunID: "run-parent", ParentToolCallID: "call-1",
	})
	if len(got) == 0 {
		t.Fatalf("got no events, want at least a SUBAGENT_STARTED")
	}
	if _, ok := got[0].(SubagentStarted); !ok {
		t.Fatalf("event 0 = %#v, want SubagentStarted (lazy start must not be PhaseStart-specific)", got[0])
	}
}

func TestMapperTranslatesRawEvent(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	got := m.Map(gantry.Event{
		Type: gantry.EventRaw, RunID: "run-1",
		RawFrame: json.RawMessage(`{"type":"ping"}`), RawSource: "anthropic",
	})
	want := []Event{newRaw(json.RawMessage(`{"type":"ping"}`), "anthropic").withIdentity(id)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %#v\nwant %#v", got, want)
	}
}

func TestMapperRawDoesNotCloseOpenTextOrReasoning(t *testing.T) {
	// Raw is metadata-like (same precedent as usage/events_dropped CUSTOM
	// events and activity events): it must not force-close an in-progress
	// text or reasoning message.
	m := NewMapper("t1", "r1")
	m.started = true

	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "run-1"})
	_ = m.Map(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "hmm", RunID: "run-1"})
	got := m.Map(gantry.Event{
		Type: gantry.EventRaw, RunID: "run-1",
		RawFrame: json.RawMessage(`{"type":"ping"}`), RawSource: "anthropic",
	})
	for _, ev := range got {
		switch ev.(type) {
		case TextMessageEnd, ReasoningMessageEnd, ReasoningEnd:
			t.Fatalf("raw event closed an open text/reasoning message: %#v", got)
		}
	}
}

func TestMapperPlanStepChangedIgnoresNilPlanStep(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true

	got := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, RunID: "run-1", PlanStep: nil})
	if len(got) != 0 {
		t.Fatalf("got %#v, want no events for a nil PlanStep", got)
	}
}

func TestMapperActivitySnapshotThenDelta(t *testing.T) {
	m := NewMapper("t1", "r1")
	m.started = true
	id := identity{RunID: "run-1"}

	step := gantry.PlanStep{ID: "s1", Description: "design", Status: gantry.StepActive}
	got1 := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, RunID: "run-1", PlanStep: &step})
	want1 := []Event{newActivitySnapshot("run-1:activity:s1", activityStepValue{ID: "s1", Description: "design", Status: "active"}).withIdentity(id)}
	if !reflect.DeepEqual(got1, want1) {
		t.Fatalf("snapshot: got  %#v\nwant %#v", got1, want1)
	}

	step.Status = gantry.StepDone
	step.Output = "spec.md"
	got2 := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, RunID: "run-1", PlanStep: &step})
	want2 := []Event{newActivityDelta("run-1:activity:s1", []jsonPatchOp{
		{Op: "replace", Path: "/status", Value: "done"},
		{Op: "replace", Path: "/output", Value: "spec.md"},
	}).withIdentity(id)}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("delta: got  %#v\nwant %#v", got2, want2)
	}

	// No change since the last report: no event at all.
	got3 := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, RunID: "run-1", PlanStep: &step})
	if len(got3) != 0 {
		t.Fatalf("unchanged: got %#v, want no events", got3)
	}
}

func TestMapperActivityDoesNotCloseOpenText(t *testing.T) {
	// Activity is metadata-like (same precedent as usage/events_dropped
	// CUSTOM events): it must not force-close an in-progress text message.
	m := NewMapper("t1", "r1")
	m.started = true

	_ = m.Map(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "run-1"}) // opens run-1:msg:1
	step := gantry.PlanStep{ID: "s1", Status: gantry.StepDone}
	got := m.Map(gantry.Event{Type: gantry.EventPlanStepChanged, RunID: "run-1", PlanStep: &step})
	for _, ev := range got {
		if _, ok := ev.(TextMessageEnd); ok {
			t.Fatalf("activity event closed the open text message: %#v", got)
		}
	}
}
