package gantry

import (
	"context"
	"encoding/json"
	"reflect"
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
	if !reflect.DeepEqual(out, in) {
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

func TestWithSinkSinkFromRoundTrip(t *testing.T) {
	var got []Event
	sink := func(ev Event) error { got = append(got, ev); return nil }

	ctx := WithSink(context.Background(), sink)
	s, ok := SinkFrom(ctx)
	if !ok || s == nil {
		t.Fatalf("SinkFrom = (%v, %v), want the installed sink", s, ok)
	}
	if err := s(Event{Type: EventDone}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if len(got) != 1 || got[0].Type != EventDone {
		t.Errorf("sink did not receive event; got %+v", got)
	}
	if _, ok := SinkFrom(context.Background()); ok {
		t.Error("SinkFrom(Background) = (_, true), want false")
	}
}

func TestWithoutSinkShadowsAmbientSink(t *testing.T) {
	calls := 0
	ctx := WithSink(context.Background(), func(Event) error { calls++; return nil })
	shadowed := WithoutSink(ctx)

	if _, ok := SinkFrom(shadowed); ok {
		t.Error("SinkFrom after WithoutSink = (_, true), want false")
	}
	// emit must be a no-op under the shadow (nil error, sink never called).
	if err := emit(shadowed, Event{Type: EventDone}); err != nil {
		t.Fatalf("emit under WithoutSink: %v", err)
	}
	if calls != 0 {
		t.Errorf("shadowed sink was called %d times, want 0", calls)
	}
	// Shadowing is scoped to the child ctx; the original is untouched.
	if _, ok := SinkFrom(ctx); !ok {
		t.Error("original ctx lost its sink after WithoutSink on a child")
	}
}

func TestWithSinkNilSinkBehavesAsWithoutSink(t *testing.T) {
	ctx := WithSink(context.Background(), nil)
	if _, ok := SinkFrom(ctx); ok {
		t.Error("SinkFrom after WithSink(ctx, nil) = (_, true), want false")
	}
}
