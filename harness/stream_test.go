package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// streamingStub verifies a type can satisfy StreamingLLMClient at compile time.
type streamingStub struct{}

func (streamingStub) Generate(_ context.Context, _ LLMRequest) (LLMResponse, error) {
	return LLMResponse{}, nil
}

func (streamingStub) GenerateStream(_ context.Context, _ LLMRequest, _ func(StreamChunk) error) (LLMResponse, error) {
	return LLMResponse{}, nil
}

var _ StreamingLLMClient = streamingStub{}

func TestEventJSONRoundTrip(t *testing.T) {
	in := Event{
		Type:        EventTextDelta,
		Iteration:   2,
		Phase:       PhaseLLMCall,
		TextDelta:   "hello",
		DoneReason:  DoneNoToolCalls,
		FinalOutput: "hello world",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Event
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out != in {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestStreamChunkOmitsEmptyFields(t *testing.T) {
	b, err := json.Marshal(StreamChunk{TextDelta: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); got != `{"text_delta":"x"}` {
		t.Errorf("StreamChunk JSON = %s, want {\"text_delta\":\"x\"}", got)
	}
}

func TestEventToolFieldsJSONShape(t *testing.T) {
	ev := Event{
		Type:      EventToolCall,
		Iteration: 1,
		ToolCall:  &ToolCall{ID: "c1", Name: "calc", Input: json.RawMessage(`{"a":2}`)},
	}
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(b)
	for _, want := range []string{`"tool_call":`, `"id":"c1"`, `"name":"calc"`, `"input":{"a":2}`} {
		if !strings.Contains(got, want) {
			t.Errorf("Event JSON %s missing %s", got, want)
		}
	}
	// No PascalCase keys leaked through.
	for _, bad := range []string{`"ID"`, `"Name"`, `"Input"`, `"CallID"`, `"IsError"`, `"Err"`} {
		if strings.Contains(got, bad) {
			t.Errorf("Event JSON %s leaked PascalCase key %s", got, bad)
		}
	}

	res := Event{
		Type:       EventToolResult,
		ToolResult: &ToolResult{CallID: "c1", Content: "5", IsError: false},
	}
	rb, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	for _, want := range []string{`"call_id":"c1"`, `"content":"5"`, `"is_error":false`} {
		if !strings.Contains(string(rb), want) {
			t.Errorf("ToolResult JSON %s missing %s", string(rb), want)
		}
	}
	// Round-trip the tool_result event back into an Event.
	var back Event
	if err := json.Unmarshal(rb, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.ToolResult == nil || back.ToolResult.CallID != "c1" || back.ToolResult.Content != "5" {
		t.Errorf("round-trip mismatch: %+v", back.ToolResult)
	}
}

func TestSinkContextRoundTrip(t *testing.T) {
	var got []Event
	sink := func(ev Event) error { got = append(got, ev); return nil }

	ctx := withSink(context.Background(), sink)
	if err := emit(ctx, Event{Type: EventDone}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(got) != 1 || got[0].Type != EventDone {
		t.Errorf("sink did not receive event; got %+v", got)
	}

	// No sink in context → emit is a no-op (nil error, nothing recorded).
	if err := emit(context.Background(), Event{Type: EventDone}); err != nil {
		t.Errorf("emit with no sink should return nil, got %v", err)
	}
}
