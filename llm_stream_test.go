package gantry_test

import (
	"context"
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
