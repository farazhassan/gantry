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
		{"text_content", newTextMessageContent("m1", "hi"), `{"type":"TEXT_MESSAGE_CONTENT","messageId":"m1","delta":"hi"}`},
		{"text_end", newTextMessageEnd("m1"), `{"type":"TEXT_MESSAGE_END","messageId":"m1"}`},
		{"tool_start", newToolCallStart("c1", "search"), `{"type":"TOOL_CALL_START","toolCallId":"c1","toolCallName":"search"}`},
		{"tool_args", newToolCallArgs("c1", `{"q":"x"}`), `{"type":"TOOL_CALL_ARGS","toolCallId":"c1","delta":"{\"q\":\"x\"}"}`},
		{"tool_end", newToolCallEnd("c1"), `{"type":"TOOL_CALL_END","toolCallId":"c1"}`},
		{"tool_result", newToolCallResult("m2", "c1", "ok"), `{"type":"TOOL_CALL_RESULT","messageId":"m2","toolCallId":"c1","content":"ok","role":"tool"}`},
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
