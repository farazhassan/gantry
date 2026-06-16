package harness_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestRunStreamEmitsTextDeltas(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(
		eval.NewMockLLMClient(harness.LLMResponse{
			Content:    "hello streaming world",
			StopReason: harness.StopReasonEnd,
		}),
	))

	var deltas strings.Builder
	state, err := a.RunStream(context.Background(), "go", func(ev harness.Event) error {
		if ev.Type == harness.EventTextDelta {
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

func (genOnlyStub) Generate(_ context.Context, _ harness.LLMRequest) (harness.LLMResponse, error) {
	return harness.LLMResponse{Content: "plain", StopReason: harness.StopReasonEnd}, nil
}

func TestRunStreamNonStreamingClientNoTextDeltas(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(genOnlyStub{}))

	var textDeltas, phases int
	state, err := a.RunStream(context.Background(), "go", func(ev harness.Event) error {
		switch ev.Type {
		case harness.EventTextDelta:
			textDeltas++
		case harness.EventPhaseStart:
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
	a, _ := harness.NewAgent(harness.WithLLM(
		eval.NewMockLLMClient(harness.LLMResponse{
			Content:    "hi",
			StopReason: harness.StopReasonEnd,
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
