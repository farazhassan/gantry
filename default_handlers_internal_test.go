package gantry

import (
	"context"
	"errors"
	"testing"
)

type stubLLM struct{ resp LLMResponse }

func (s stubLLM) Generate(context.Context, LLMRequest) (LLMResponse, error) { return s.resp, nil }

type erroringLLM struct {
	resp LLMResponse
	err  error
}

func (e erroringLLM) Generate(context.Context, LLMRequest) (LLMResponse, error) { return e.resp, e.err }

type recordingLLM struct {
	resp    LLMResponse
	capture *LLMRequest
}

func (r recordingLLM) Generate(_ context.Context, req LLMRequest) (LLMResponse, error) {
	*r.capture = req
	return r.resp, nil
}

type streamStubLLM struct {
	resp   LLMResponse
	deltas []string
}

func (s streamStubLLM) Generate(context.Context, LLMRequest) (LLMResponse, error) { return s.resp, nil }
func (s streamStubLLM) GenerateStream(_ context.Context, _ LLMRequest, yield func(StreamChunk) error) (LLMResponse, error) {
	for _, d := range s.deltas {
		if err := yield(StreamChunk{TextDelta: d}); err != nil {
			return LLMResponse{}, err
		}
	}
	return s.resp, nil
}

// findEndedSpan returns the KindSpanEnd event for the named span.
func findEndedSpan(t *testing.T, tr *Trace, name string) TraceEvent {
	t.Helper()
	for _, ev := range tr.Snapshot() {
		if ev.Kind == KindSpanEnd && ev.Name == name {
			return ev
		}
	}
	t.Fatalf("no ended %q span in trace", name)
	return TraceEvent{}
}

func TestDefaultLLMCallHandler_EmitsGenerationSpan(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	ctx := withTracer(context.Background(), tr)
	ctx, root := tr.StartSpan(ctx, "run")

	h := DefaultLLMCallHandler(stubLLM{resp: LLMResponse{
		Content: "hello", Usage: Usage{InputTokens: 11, OutputTokens: 3}, Model: "qwen",
	}})
	state := &State{System: "sys", Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	root.End(nil)

	gen := findEndedSpan(t, trc, "model.call")
	if gen.Attrs[SpanKindKey] != SpanKindGeneration {
		t.Fatalf("gantry.kind = %v, want %q", gen.Attrs[SpanKindKey], SpanKindGeneration)
	}
	if gen.Attrs["usage_in"] != 11 || gen.Attrs["usage_out"] != 3 {
		t.Fatalf("usage attrs wrong: %v / %v", gen.Attrs["usage_in"], gen.Attrs["usage_out"])
	}
	if gen.Attrs["model"] != "qwen" {
		t.Fatalf("model attr = %v", gen.Attrs["model"])
	}
	in, _ := gen.Attrs["input"].(string)
	if in == "" || gen.Attrs["output"] == "" {
		t.Fatalf("input/output not recorded: in=%q out=%v", in, gen.Attrs["output"])
	}
	// still records LastResponse/Usage on state
	if state.LastResponse == nil || state.Usage.InputTokens != 11 {
		t.Fatalf("state not updated: %+v", state)
	}
}

func TestDefaultLLMCallHandler_StreamingStillRecordsGeneration(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	var streamed string
	ctx := withTracer(context.Background(), tr)
	ctx = WithSink(ctx, func(ev Event) error { streamed += ev.TextDelta; return nil })
	ctx, root := tr.StartSpan(ctx, "run")

	h := DefaultLLMCallHandler(streamStubLLM{
		resp:   LLMResponse{Content: "Hello", Usage: Usage{InputTokens: 1, OutputTokens: 2}, Model: "m"},
		deltas: []string{"Hel", "lo"},
	})
	state := &State{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	root.End(nil)

	if streamed != "Hello" {
		t.Fatalf("deltas not streamed: %q", streamed)
	}
	gen := findEndedSpan(t, trc, "model.call")
	if gen.Attrs[SpanKindKey] != SpanKindGeneration || gen.Attrs[AttrOutput] == "" {
		t.Fatalf("streaming generation span incomplete: %v", gen.Attrs)
	}
}

func TestDefaultLLMCallHandler_UsesContextTemperature(t *testing.T) {
	ctx := withTemperature(context.Background(), 0.9)

	var gotReq LLMRequest
	h := DefaultLLMCallHandler(recordingLLM{
		resp:    LLMResponse{Content: "hi"},
		capture: &gotReq,
	})
	state := &State{System: "sys", Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if gotReq.Temperature != 0.9 {
		t.Errorf("req.Temperature = %v, want 0.9", gotReq.Temperature)
	}
}

func TestDefaultLLMCallHandler_NoContextTemperatureDefaultsToZero(t *testing.T) {
	var gotReq LLMRequest
	h := DefaultLLMCallHandler(recordingLLM{
		resp:    LLMResponse{Content: "hi"},
		capture: &gotReq,
	})
	state := &State{System: "sys", Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(context.Background(), state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if gotReq.Temperature != 0 {
		t.Errorf("req.Temperature = %v, want 0", gotReq.Temperature)
	}
}

func TestDefaultLLMCallHandler_PassesToolChoiceFromContext(t *testing.T) {
	var got LLMRequest
	h := DefaultLLMCallHandler(recordingLLM{resp: LLMResponse{Content: "ok"}, capture: &got})
	ctx := withToolChoice(context.Background(), &ToolChoice{Mode: ToolChoiceNone})
	state := &State{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got.ToolChoice == nil || got.ToolChoice.Mode != ToolChoiceNone {
		t.Fatalf("ToolChoice = %+v, want Mode none", got.ToolChoice)
	}
}

func TestDefaultLLMCallHandler_NilToolChoiceByDefault(t *testing.T) {
	var got LLMRequest
	h := DefaultLLMCallHandler(recordingLLM{resp: LLMResponse{Content: "ok"}, capture: &got})
	state := &State{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(context.Background(), state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got.ToolChoice != nil {
		t.Fatalf("ToolChoice = %+v, want nil", got.ToolChoice)
	}
}

func TestToolChoiceFrom_BareContextIsNil(t *testing.T) {
	if tc := ToolChoiceFrom(context.Background()); tc != nil {
		t.Fatalf("ToolChoiceFrom(bare) = %+v, want nil", tc)
	}
}

func TestDefaultLLMCallHandler_RecordsCostAndProvider(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	ctx := withTracer(context.Background(), tr)
	ctx, root := tr.StartSpan(ctx, "run")

	h := DefaultLLMCallHandler(stubLLM{resp: LLMResponse{
		Content:  "hello",
		Usage:    Usage{InputTokens: 11, OutputTokens: 3, Cost: 0.125},
		Provider: "Baidu",
	}})
	state := &State{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	root.End(nil)

	gen := findEndedSpan(t, trc, "model.call")
	if gen.Attrs[AttrUsageCost] != 0.125 {
		t.Errorf("usage_cost = %v, want 0.125", gen.Attrs[AttrUsageCost])
	}
	if gen.Attrs[AttrProvider] != "Baidu" {
		t.Errorf("provider = %v, want Baidu", gen.Attrs[AttrProvider])
	}
}

func TestDefaultLLMCallHandler_OmitsZeroCostAndEmptyProvider(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	ctx := withTracer(context.Background(), tr)
	ctx, root := tr.StartSpan(ctx, "run")

	h := DefaultLLMCallHandler(stubLLM{resp: LLMResponse{
		Content: "hello", Usage: Usage{InputTokens: 11, OutputTokens: 3},
	}})
	state := &State{Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); err != nil {
		t.Fatalf("handler: %v", err)
	}
	root.End(nil)

	gen := findEndedSpan(t, trc, "model.call")
	if v, ok := gen.Attrs[AttrUsageCost]; ok {
		t.Errorf("usage_cost = %v, want attr absent when cost is 0", v)
	}
	if v, ok := gen.Attrs[AttrProvider]; ok {
		t.Errorf("provider = %v, want attr absent when provider is empty", v)
	}
}

func TestDefaultLLMCallHandler_CountsUsageSpentByAFailedCall(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	ctx := withTracer(context.Background(), tr)
	ctx, root := tr.StartSpan(ctx, "run")

	boom := errors.New("boom")
	h := DefaultLLMCallHandler(erroringLLM{
		resp: LLMResponse{Usage: Usage{InputTokens: 100, OutputTokens: 7, Cost: 0.25}, Model: "m", Provider: "p"},
		err:  boom,
	})
	state := &State{Usage: Usage{InputTokens: 1, Cost: 0.5}, Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	if err := h(ctx, state); !errors.Is(err, boom) {
		t.Fatalf("handler err = %v, want boom", err)
	}
	root.End(nil)

	want := Usage{InputTokens: 101, OutputTokens: 7, Cost: 0.75}
	if state.Usage != want {
		t.Errorf("State.Usage = %+v, want %+v (prior plus the failed call's spend)", state.Usage, want)
	}
	if state.LastResponse != nil {
		t.Errorf("LastResponse = %+v, want nil: a failed call must not become the last reply", state.LastResponse)
	}
	gen := findEndedSpan(t, trc, "model.call")
	if gen.Attrs[AttrUsageIn] != 100 || gen.Attrs[AttrUsageOut] != 7 || gen.Attrs[AttrUsageCost] != 0.25 {
		t.Errorf("failed span usage attrs = %v/%v/%v, want 100/7/0.25", gen.Attrs[AttrUsageIn], gen.Attrs[AttrUsageOut], gen.Attrs[AttrUsageCost])
	}
	for _, k := range []string{AttrOutput, AttrModel, AttrProvider} {
		if v, ok := gen.Attrs[k]; ok {
			t.Errorf("failed span has %s = %v, want it success-only", k, v)
		}
	}
}

func TestDefaultLLMCallHandler_FailedCallWithoutUsageRecordsNone(t *testing.T) {
	trc := NewTrace()
	tr := NewDefaultTracer(trc)
	ctx := withTracer(context.Background(), tr)
	ctx, root := tr.StartSpan(ctx, "run")

	h := DefaultLLMCallHandler(erroringLLM{err: errors.New("boom")})
	state := &State{Usage: Usage{InputTokens: 1}, Messages: []Message{{Role: RoleUser, Content: "hi"}}}
	_ = h(ctx, state)
	root.End(nil)

	if state.Usage != (Usage{InputTokens: 1}) {
		t.Errorf("State.Usage = %+v, want unchanged", state.Usage)
	}
	gen := findEndedSpan(t, trc, "model.call")
	for _, k := range []string{AttrUsageIn, AttrUsageOut, AttrUsageCost} {
		if v, ok := gen.Attrs[k]; ok {
			t.Errorf("failed span with no usage has %s = %v, want absent", k, v)
		}
	}
}
