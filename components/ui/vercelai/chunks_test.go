package vercelai

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestChunkJSONShape(t *testing.T) {
	cases := []struct {
		name string
		c    Chunk
		want string
	}{
		{"start", newStart("m1"), `{"type":"start","messageId":"m1"}`},
		{"finish", newFinish(), `{"type":"finish"}`},
		{"text_start", newTextStart("m1:text:1"), `{"type":"text-start","id":"m1:text:1"}`},
		{"text_delta", newTextDelta("m1:text:1", "hi"), `{"type":"text-delta","id":"m1:text:1","delta":"hi"}`},
		{"text_end", newTextEnd("m1:text:1"), `{"type":"text-end","id":"m1:text:1"}`},
		{"reasoning_start", newReasoningStart("m1:reasoning:1"), `{"type":"reasoning-start","id":"m1:reasoning:1"}`},
		{"reasoning_delta", newReasoningDelta("m1:reasoning:1", "hmm"), `{"type":"reasoning-delta","id":"m1:reasoning:1","delta":"hmm"}`},
		{"reasoning_end", newReasoningEnd("m1:reasoning:1"), `{"type":"reasoning-end","id":"m1:reasoning:1"}`},
		{"tool_input_start", newToolInputStart("c1", "search"), `{"type":"tool-input-start","toolCallId":"c1","toolName":"search","dynamic":true}`},
		{"tool_input_available", newToolInputAvailable("c1", "search", json.RawMessage(`{"q":"x"}`)), `{"type":"tool-input-available","toolCallId":"c1","toolName":"search","input":{"q":"x"},"dynamic":true}`},
		{"tool_input_available_empty_input", newToolInputAvailable("c1", "search", nil), `{"type":"tool-input-available","toolCallId":"c1","toolName":"search","input":{},"dynamic":true}`},
		{"tool_output_available", newToolOutputAvailable("c1", "ok"), `{"type":"tool-output-available","toolCallId":"c1","output":"ok"}`},
		{"tool_output_error", newToolOutputError("c1", "boom"), `{"type":"tool-output-error","toolCallId":"c1","errorText":"boom"}`},
		{"start_step", newStartStep(), `{"type":"start-step"}`},
		{"finish_step", newFinishStep(), `{"type":"finish-step"}`},
		{"error", newErrorChunk("boom"), `{"type":"error","errorText":"boom"}`},
		{"data_usage", newDataChunk("gantry-usage", "", usageValue{InputTokens: 120, OutputTokens: 340, Tokens: 460, CostUSD: 0.0041}), `{"type":"data-gantry-usage","data":{"inputTokens":120,"outputTokens":340,"tokens":460,"costUsd":0.0041}}`},
		{"data_usage_zero_cost", newDataChunk("gantry-usage", "", usageValue{InputTokens: 10, Tokens: 10}), `{"type":"data-gantry-usage","data":{"inputTokens":10,"tokens":10}}`},
		{"data_events_dropped", newDataChunk("gantry-events-dropped", "", eventsDroppedValue{Count: 3}), `{"type":"data-gantry-events-dropped","data":{"count":3}}`},
		{
			"data_plan_step",
			newDataChunk("gantry-plan-step", "s1", planStepValue{ID: "s1", Description: "design", Status: "active", Output: ""}),
			`{"type":"data-gantry-plan-step","id":"s1","data":{"id":"s1","description":"design","status":"active","output":""}}`,
		},
		{
			"data_raw",
			newDataChunk("gantry-raw", "", rawValue{Event: json.RawMessage(`{"type":"ping"}`), Source: "anthropic"}),
			`{"type":"data-gantry-raw","data":{"event":{"type":"ping"},"source":"anthropic"}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.c)
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
	if err := WriteSSE(&buf, newFinish()); err != nil {
		t.Fatalf("WriteSSE: %v", err)
	}
	want := "data: {\"type\":\"finish\"}\n\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}

func TestWriteDone(t *testing.T) {
	var buf bytes.Buffer
	if err := writeDone(&buf); err != nil {
		t.Fatalf("writeDone: %v", err)
	}
	want := "data: [DONE]\n\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}
