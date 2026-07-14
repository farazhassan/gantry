package gantry_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// newSubAgentParent builds a parent agent whose PhaseToolExec middleware runs a
// nested sub-agent via plain Run, wrapping the nested run's ctx with wrapCtx.
// The parent's first turn issues a tool call so PhaseToolExec actually runs;
// the sub-agent's mock streams distinctive text ("SUBAGE" is its first 6-rune
// delta) so a leak into the parent's stream is detectable.
func newSubAgentParent(t *testing.T, wrapCtx func(context.Context) context.Context) *gantry.Agent {
	t.Helper()
	sub, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "SUBAGENT-OUTPUT", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent(sub): %v", err)
	}
	parent, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "c1", Name: "delegate", Input: []byte(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "parent done", StopReason: gantry.StopReasonEnd},
	)))
	if err != nil {
		t.Fatalf("NewAgent(parent): %v", err)
	}
	parent.Use(gantry.PhaseToolExec, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if _, err := sub.Run(wrapCtx(ctx), "sub goal"); err != nil {
				return err
			}
			return next(ctx, s)
		}
	})
	return parent
}

// subLeaked reports whether any text delta from the sub-agent reached sink'd
// events (the sub streams because the ambient sink makes invokeLLM stream).
func subLeaked(events []gantry.Event) bool {
	for _, ev := range events {
		if ev.Type == gantry.EventTextDelta && strings.Contains(ev.TextDelta, "SUB") {
			return true
		}
	}
	return false
}

func TestNestedRunInheritsAmbientSinkWithoutShadowing(t *testing.T) {
	// Documents the hazard WithoutSink exists to fix: a nested agent run
	// reached through the parent's streaming ctx interleaves its events
	// (including its text deltas) into the parent's sink.
	parent := newSubAgentParent(t, func(ctx context.Context) context.Context { return ctx })

	var events []gantry.Event
	if _, err := parent.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if !subLeaked(events) {
		t.Error("expected the nested run's deltas to leak into the parent sink (the documented hazard)")
	}
}

func TestWithoutSinkIsolatesNestedRun(t *testing.T) {
	parent := newSubAgentParent(t, gantry.WithoutSink)

	var events []gantry.Event
	if _, err := parent.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		events = append(events, ev)
		return nil
	}); err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	if subLeaked(events) {
		t.Error("nested run's deltas leaked into the parent sink despite WithoutSink")
	}
}
