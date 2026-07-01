package gantry

import (
	"context"
	"testing"
)

type stubLLM struct{ resp LLMResponse }

func (s stubLLM) Generate(context.Context, LLMRequest) (LLMResponse, error) { return s.resp, nil }

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
	if gen.Attrs["observation.type"] != "generation" {
		t.Fatalf("observation.type = %v, want generation", gen.Attrs["observation.type"])
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
	ctx = withSink(ctx, func(ev Event) error { streamed += ev.TextDelta; return nil })
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
	if gen.Attrs["observation.type"] != "generation" || gen.Attrs["output"] == "" {
		t.Fatalf("streaming generation span incomplete: %v", gen.Attrs)
	}
}
