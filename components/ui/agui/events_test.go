package agui

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEventJSONShape(t *testing.T) {
	cases := []struct {
		name string
		ev   Event
		want string
	}{
		{"run_started", newRunStarted("t1", "r1"), `{"type":"RUN_STARTED","threadId":"t1","runId":"r1"}`},
		{"run_finished", newRunFinished("t1", "r1"), `{"type":"RUN_FINISHED","threadId":"t1","runId":"r1"}`},
		{"run_error", newRunError("boom"), `{"type":"RUN_ERROR","message":"boom"}`},
		{"step_started", newStepStarted("llm_call"), `{"type":"STEP_STARTED","stepName":"llm_call"}`},
		{"step_finished", newStepFinished("llm_call"), `{"type":"STEP_FINISHED","stepName":"llm_call"}`},
		{"text_start", newTextMessageStart("m1"), `{"type":"TEXT_MESSAGE_START","messageId":"m1","role":"assistant"}`},
		{
			"text_start_with_subagent_attribution",
			newTextMessageStart("m1").withIdentity(identity{RunID: "run-child", SubagentRunID: "run-child"}),
			`{"type":"TEXT_MESSAGE_START","messageId":"m1","role":"assistant","runId":"run-child","subagentRunId":"run-child"}`,
		},
		{"text_content", newTextMessageContent("m1", "hi"), `{"type":"TEXT_MESSAGE_CONTENT","messageId":"m1","delta":"hi"}`},
		{"text_end", newTextMessageEnd("m1"), `{"type":"TEXT_MESSAGE_END","messageId":"m1"}`},
		{"reasoning_start", newReasoningStart("r1"), `{"type":"REASONING_START","messageId":"r1"}`},
		{"reasoning_msg_start", newReasoningMessageStart("r1"), `{"type":"REASONING_MESSAGE_START","messageId":"r1","role":"reasoning"}`},
		{"reasoning_msg_content", newReasoningMessageContent("r1", "hmm"), `{"type":"REASONING_MESSAGE_CONTENT","messageId":"r1","delta":"hmm"}`},
		{"reasoning_msg_end", newReasoningMessageEnd("r1"), `{"type":"REASONING_MESSAGE_END","messageId":"r1"}`},
		{"reasoning_end", newReasoningEnd("r1"), `{"type":"REASONING_END","messageId":"r1"}`},
		{"tool_start", newToolCallStart("c1", "search"), `{"type":"TOOL_CALL_START","toolCallId":"c1","toolCallName":"search"}`},
		{"tool_args", newToolCallArgs("c1", `{"q":"x"}`), `{"type":"TOOL_CALL_ARGS","toolCallId":"c1","delta":"{\"q\":\"x\"}"}`},
		{"tool_end", newToolCallEnd("c1"), `{"type":"TOOL_CALL_END","toolCallId":"c1"}`},
		{"tool_result", newToolCallResult("m2", "c1", "ok", false), `{"type":"TOOL_CALL_RESULT","messageId":"m2","toolCallId":"c1","content":"ok","role":"tool"}`},
		{"tool_result_error", newToolCallResult("m2", "c1", "boom", true), `{"type":"TOOL_CALL_RESULT","messageId":"m2","toolCallId":"c1","content":"boom","role":"tool","isError":true}`},
		{"raw", newRaw(json.RawMessage(`{"type":"ping"}`), "anthropic"), `{"type":"RAW","event":{"type":"ping"},"source":"anthropic"}`},
		{
			"activity_snapshot",
			newActivitySnapshot("run-1:activity:s1", activityStepValue{ID: "s1", Description: "design", Status: "active"}),
			`{"type":"ACTIVITY_SNAPSHOT","messageId":"run-1:activity:s1","activityType":"gantry.plan_step","content":{"id":"s1","description":"design","status":"active","output":""}}`,
		},
		{
			"activity_delta",
			newActivityDelta("run-1:activity:s1", []jsonPatchOp{{Op: "replace", Path: "/status", Value: "done"}}),
			`{"type":"ACTIVITY_DELTA","messageId":"run-1:activity:s1","activityType":"gantry.plan_step","patch":[{"op":"replace","path":"/status","value":"done"}]}`,
		},
		{"usage", newUsage(120, 340, 0.0041, identity{}), `{"type":"CUSTOM","name":"gantry.usage","value":{"inputTokens":120,"outputTokens":340,"tokens":460,"costUsd":0.0041}}`},
		{"usage_zero_cost", newUsage(10, 0, 0, identity{}), `{"type":"CUSTOM","name":"gantry.usage","value":{"inputTokens":10,"tokens":10}}`},
		{"events_dropped", newEventsDropped(3, identity{}), `{"type":"CUSTOM","name":"gantry.events_dropped","value":{"count":3}}`},
		{"subagent_started_minimal", newSubagentStarted("run-child", "researcher", "", ""), `{"type":"SUBAGENT_STARTED","subagentRunId":"run-child","name":"researcher"}`},
		{
			"subagent_started_full",
			newSubagentStarted("run-child", "researcher", "Delegates fact-finding", "run-grandparent").withIdentity(identity{RunID: "run-child", Agent: "investigation", ParentRunID: "run-parent", ParentToolCallID: "call-1"}),
			`{"type":"SUBAGENT_STARTED","subagentRunId":"run-child","name":"researcher","description":"Delegates fact-finding","parentSubagentRunId":"run-grandparent","runId":"run-child","agent":"investigation","parentRunId":"run-parent","parentToolCallId":"call-1"}`,
		},
		{"subagent_finished_success", newSubagentFinished("run-child", "42 facts found", false), `{"type":"SUBAGENT_FINISHED","subagentRunId":"run-child","result":"42 facts found"}`},
		{"subagent_finished_suspended", newSubagentFinished("run-child", "", true), `{"type":"SUBAGENT_FINISHED","subagentRunId":"run-child","outcome":{"type":"suspended"}}`},
		{"subagent_error", newSubagentError("run-child", "sub-agent run terminated: guardrail_blocked"), `{"type":"SUBAGENT_ERROR","subagentRunId":"run-child","message":"sub-agent run terminated: guardrail_blocked"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.ev)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(b) != tc.want {
				t.Fatalf("got  %s\nwant %s", b, tc.want)
			}
		})
	}
}

func TestWriteSSE(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteSSE(&buf, newRunFinished("t1", "r1")); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	want := "data: {\"type\":\"RUN_FINISHED\",\"threadId\":\"t1\",\"runId\":\"r1\"}\n\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}

// TestSubagentEventsExplicitSubagentRunIDWinsOverEmbeddedIdentity documents
// a deliberate field collision: SubagentStarted/Finished/Error embed
// identity (for gantry's own RunID/Agent/... richness) AND declare their
// own required, non-omitempty top-level SubagentRunID -- the same JSON key
// ("subagentRunId") exists at two struct-nesting depths. Go's encoding/json
// resolves this by promoting the shallower (depth-0, explicit) field and
// silently dropping the deeper (depth-1, embedded) one from output -- this
// is what makes the apparent duplication safe rather than an ambiguous-key
// bug. In real Mapper usage both are always set to the same value anyway
// (see mapper.go), so this only matters for a reader wondering why the
// duplication isn't "fixed".
func TestSubagentEventsExplicitSubagentRunIDWinsOverEmbeddedIdentity(t *testing.T) {
	ev := SubagentStarted{Type: typeSubagentStarted, SubagentRunID: "top-level-wins", Name: "researcher"}
	ev.identity = identity{SubagentRunID: "embedded-should-be-ignored"}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"type":"SUBAGENT_STARTED","subagentRunId":"top-level-wins","name":"researcher"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}
