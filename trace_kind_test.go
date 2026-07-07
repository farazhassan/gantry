package gantry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestRunAndPhaseSpansCarryKind(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "hi",
		StopReason: gantry.StopReasonEnd,
	})
	a, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawAgent, sawPhase bool
	for _, ev := range state.Trace.Snapshot() {
		if ev.Kind != gantry.KindSpanEnd {
			continue
		}
		switch {
		case ev.Name == "run":
			if ev.Attrs[gantry.SpanKindKey] != gantry.SpanKindAgent {
				t.Errorf("run kind = %v, want %q", ev.Attrs[gantry.SpanKindKey], gantry.SpanKindAgent)
			}
			sawAgent = true
		case strings.HasPrefix(ev.Name, "phase:"):
			if ev.Attrs[gantry.SpanKindKey] != gantry.SpanKindPhase {
				t.Errorf("phase %q kind = %v, want %q", ev.Name, ev.Attrs[gantry.SpanKindKey], gantry.SpanKindPhase)
			}
			sawPhase = true
		}
	}
	if !sawAgent {
		t.Error("no run span found in trace")
	}
	if !sawPhase {
		t.Error("no phase span found in trace")
	}
}
