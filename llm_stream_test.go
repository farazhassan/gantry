package gantry_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestRunStreamEmitsTextDeltas(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(
		eval.NewMockLLMClient(gantry.LLMResponse{
			Content:    "hello streaming world",
			StopReason: gantry.StopReasonEnd,
		}),
	))

	var deltas strings.Builder
	state, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		if ev.Type == gantry.EventTextDelta {
			deltas.WriteString(ev.TextDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if deltas.String() != "hello streaming world" {
		t.Errorf("concatenated deltas = %q, want %q", deltas.String(), "hello streaming world")
	}
	if state.FinalOutput != deltas.String() {
		t.Errorf("FinalOutput %q != concatenated deltas %q", state.FinalOutput, deltas.String())
	}
}

// genOnlyStub implements LLMClient but NOT StreamingLLMClient.
type genOnlyStub struct{}

func (genOnlyStub) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{Content: "plain", StopReason: gantry.StopReasonEnd}, nil
}

func TestRunStreamNonStreamingClientNoTextDeltas(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(genOnlyStub{}))

	var textDeltas, phases int
	state, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		switch ev.Type {
		case gantry.EventTextDelta:
			textDeltas++
		case gantry.EventPhaseStart:
			phases++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if textDeltas != 0 {
		t.Errorf("text delta events = %d, want 0 (non-streaming client)", textDeltas)
	}
	if phases == 0 {
		t.Error("expected phase_start events even for a non-streaming client")
	}
	if state.FinalOutput != "plain" {
		t.Errorf("FinalOutput = %q, want %q", state.FinalOutput, "plain")
	}
}

func TestRunWithStreamingClientNoSinkUsesGenerate(t *testing.T) {
	// A streaming-capable mock, but plain Run (no sink) must use Generate and
	// behave exactly as before.
	a, _ := gantry.NewAgent(gantry.WithLLM(
		eval.NewMockLLMClient(gantry.LLMResponse{
			Content:    "hi",
			StopReason: gantry.StopReasonEnd,
		}),
	))
	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "hi" {
		t.Errorf("FinalOutput = %q, want %q", state.FinalOutput, "hi")
	}
}

// reasoningRawStub is a StreamingLLMClient stub that yields a reasoning-delta
// chunk and a raw-frame chunk alongside a text-delta chunk, to exercise
// invokeLLM's per-field emission branches independently (a single-field gate
// on TextDelta would silently drop the other two).
type reasoningRawStub struct{}

func (reasoningRawStub) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd}, nil
}

func (reasoningRawStub) GenerateStream(_ context.Context, _ gantry.LLMRequest, yield func(gantry.StreamChunk) error) (gantry.LLMResponse, error) {
	if err := yield(gantry.StreamChunk{ReasoningDelta: "pondering..."}); err != nil {
		return gantry.LLMResponse{}, err
	}
	if err := yield(gantry.StreamChunk{RawFrame: json.RawMessage(`{"type":"ping"}`), RawSource: "test"}); err != nil {
		return gantry.LLMResponse{}, err
	}
	if err := yield(gantry.StreamChunk{TextDelta: "hi"}); err != nil {
		return gantry.LLMResponse{}, err
	}
	return gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd}, nil
}

func TestRunStreamEmitsReasoningAndRawEvents(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(reasoningRawStub{}))

	var reasoning []string
	var raw []gantry.Event
	var textDeltas []string
	_, err := a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		switch ev.Type {
		case gantry.EventReasoningDelta:
			reasoning = append(reasoning, ev.ReasoningDelta)
		case gantry.EventRaw:
			raw = append(raw, ev)
		case gantry.EventTextDelta:
			textDeltas = append(textDeltas, ev.TextDelta)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if len(reasoning) != 1 || reasoning[0] != "pondering..." {
		t.Errorf("reasoning deltas = %v, want [\"pondering...\"]", reasoning)
	}
	if len(raw) != 1 || string(raw[0].RawFrame) != `{"type":"ping"}` || raw[0].RawSource != "test" {
		t.Errorf("raw events = %+v, want one {\"type\":\"ping\"} from source \"test\"", raw)
	}
	if len(textDeltas) != 1 || textDeltas[0] != "hi" {
		t.Errorf("text deltas = %v, want [\"hi\"]", textDeltas)
	}
}
