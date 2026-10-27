package gantry_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/farazhassan/gantry"
)

// startingStub's first streamed reply announces a call to each tool in
// calls (ToolCallStart), then returns them as the response's tool calls;
// every later reply is a plain answer.
type startingStub struct {
	calls   []gantry.ToolCall
	replied bool
}

func (s *startingStub) Generate(context.Context, gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd}, nil
}

func (s *startingStub) GenerateStream(ctx context.Context, req gantry.LLMRequest, yield func(gantry.StreamChunk) error) (gantry.LLMResponse, error) {
	if s.replied {
		return s.Generate(ctx, req)
	}
	s.replied = true
	for _, tc := range s.calls {
		if err := yield(gantry.StreamChunk{ToolCallStart: &gantry.ToolCall{ID: tc.ID, Name: tc.Name}}); err != nil {
			return gantry.LLMResponse{}, err
		}
	}
	return gantry.LLMResponse{ToolCalls: s.calls, StopReason: gantry.StopReasonToolUse}, nil
}

func TestRunStreamEmitsToolCallStartOnlyForEarlyStartTools(t *testing.T) {
	calls := []gantry.ToolCall{
		{ID: "c1", Name: "submit", Input: json.RawMessage(`{}`)},
		{ID: "c2", Name: "lookup", Input: json.RawMessage(`{}`)},
	}
	a, err := gantry.NewAgent(gantry.WithLLM(&startingStub{calls: calls}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	a.Use(gantry.PhaseStart, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Tools = append(s.Tools,
				gantry.ToolDef{Name: "submit", EarlyStart: true},
				gantry.ToolDef{Name: "lookup"},
			)
			return next(ctx, s)
		}
	})

	var got []string
	_, _ = a.RunStream(context.Background(), "go", func(ev gantry.Event) error {
		switch ev.Type {
		case gantry.EventToolCallStart:
			if ev.Phase != gantry.PhaseLLMCall || ev.ToolCall.Input != nil {
				t.Errorf("tool_call_start = %+v, want phase llm_call and no input", ev)
			}
			got = append(got, "start:"+ev.ToolCall.ID)
		case gantry.EventToolCall:
			got = append(got, "call:"+ev.ToolCall.ID)
		}
		return nil
	})
	want := []string{"start:c1", "call:c1", "call:c2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}
