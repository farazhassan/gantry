package gantry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func overflowErr() error {
	return &gantry.ContextLengthError{Limit: 100, Err: errors.New("too long")}
}

func okResp() gantry.LLMResponse {
	return gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}
}

func TestOverflowHandlerRerunsWholeLLMPhaseOnce(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: overflowErr()}, {Response: okResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	outer := 0
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error { outer++; return next(ctx, s) }
	})
	calls := 0
	if err := a.OnContextOverflow(func(ctx context.Context, s *gantry.State, err error) (bool, error) {
		calls++
		if !errors.Is(err, gantry.ErrContextLengthExceeded) {
			t.Errorf("handler err = %v", err)
		}
		return true, nil
	}); err != nil {
		t.Fatalf("OnContextOverflow: %v", err)
	}
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls != 1 || outer != 2 || len(mock.Requests()) != 2 {
		t.Errorf("handler calls=%d, middleware runs=%d, LLM calls=%d; want 1, 2, 2", calls, outer, len(mock.Requests()))
	}
}

func TestOverflowHandlerRetriesOnlyOnce(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: overflowErr()}, {Err: overflowErr()}, {Response: okResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { return true, nil })
	_, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) || len(mock.Requests()) != 2 {
		t.Errorf("err = %v, LLM calls = %d; want overflow error after 2 calls", err, len(mock.Requests()))
	}
}

func TestOverflowHandlerDeclinesOrFails(t *testing.T) {
	hErr := errors.New("handler failed")
	for name, h := range map[string]gantry.ContextOverflowHandler{
		"decline": func(context.Context, *gantry.State, error) (bool, error) { return false, nil },
		"error":   func(context.Context, *gantry.State, error) (bool, error) { return true, hErr },
	} {
		mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: overflowErr()}, {Response: okResp()}})
		a, _ := gantry.NewAgent(gantry.WithLLM(mock))
		_ = a.OnContextOverflow(h)
		_, err := a.Run(context.Background(), "hi")
		if len(mock.Requests()) != 1 {
			t.Errorf("%s: LLM calls = %d, want 1", name, len(mock.Requests()))
		}
		if name == "decline" && !errors.Is(err, gantry.ErrContextLengthExceeded) {
			t.Errorf("decline: err = %v, want original overflow", err)
		}
		if name == "error" && (!errors.Is(err, hErr) || !errors.Is(err, gantry.ErrContextLengthExceeded)) {
			t.Errorf("error: err = %v, want both the handler error and the overflow", err)
		}
	}
}

func TestOverflowHandlerNotCalledForOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: boom}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	called := false
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { called = true; return true, nil })
	if _, err := a.Run(context.Background(), "hi"); !errors.Is(err, boom) || called {
		t.Errorf("err = %v, handler called = %v; want boom, false", err, called)
	}
}

func TestOnContextOverflowRejectsNilAndSecondHandler(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
	if err := a.OnContextOverflow(nil); err == nil {
		t.Error("nil handler: want error")
	}
	h := func(context.Context, *gantry.State, error) (bool, error) { return false, nil }
	if err := a.OnContextOverflow(h); err != nil {
		t.Fatalf("first handler: %v", err)
	}
	if err := a.OnContextOverflow(h); err == nil {
		t.Error("second handler: want error")
	}
}

func cutOffToolResp() gantry.LLMResponse {
	return gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: "t1", Name: "noop", Input: []byte(`{"q":`)}},
		StopReason: gantry.StopReasonContextWindow,
		Usage:      gantry.Usage{InputTokens: 90, OutputTokens: 10},
	}
}

func TestContextWindowStopWithToolCallsIsOverflow(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Response: cutOffToolResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	s, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Fatalf("Run err = %v, want ErrContextLengthExceeded", err)
	}
	if len(s.PendingToolCalls) != 0 || s.LastResponse != nil {
		t.Errorf("cut-off tool call reached the loop: pending=%v last=%v", s.PendingToolCalls, s.LastResponse)
	}
	if s.Usage.InputTokens != 90 || s.Usage.OutputTokens != 10 {
		t.Errorf("Usage = %+v, want the spent tokens recorded", s.Usage)
	}
}

func TestContextWindowStopWithToolCallsRetriesViaHandler(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Response: cutOffToolResp()}, {Response: okResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { return true, nil })
	s, err := a.Run(context.Background(), "hi")
	if err != nil || s.FinalOutput != "ok" {
		t.Errorf("Run = %q, %v; want ok after one retry", s.FinalOutput, err)
	}
	for _, m := range s.Messages {
		if len(m.ToolCalls) > 0 || m.Role == gantry.RoleTool {
			t.Errorf("cut-off tool call reached the transcript: %+v", s.Messages)
		}
	}
}

func TestContextWindowStopWithoutToolCallsIsNormal(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "partial", StopReason: gantry.StopReasonContextWindow})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	s, err := a.Run(context.Background(), "hi")
	if err != nil || s.FinalOutput != "partial" {
		t.Errorf("Run = %q, %v; want partial, nil", s.FinalOutput, err)
	}
}

func TestContextWindowStopAfterStreamedTextIsNotRetried(t *testing.T) {
	cut := cutOffToolResp()
	cut.Content = "partial text"
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Response: cut}, {Response: okResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	called := false
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { called = true; return true, nil })
	var deltas []string
	_, err := a.RunStream(context.Background(), "hi", func(ev gantry.Event) error {
		if ev.Type == gantry.EventTextDelta {
			deltas = append(deltas, ev.TextDelta)
		}
		return nil
	})
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Fatalf("RunStream err = %v, want ErrContextLengthExceeded", err)
	}
	if called || len(mock.Requests()) != 1 {
		t.Errorf("handler called = %v, LLM calls = %d; want no retry once text was streamed (deltas %q)", called, len(mock.Requests()), deltas)
	}
}

func TestContextWindowStopWithoutStreamedTextStillRetriesInStream(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Response: cutOffToolResp()}, {Response: okResp()}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.OnContextOverflow(func(context.Context, *gantry.State, error) (bool, error) { return true, nil })
	s, err := a.RunStream(context.Background(), "hi", func(gantry.Event) error { return nil })
	if err != nil || s.FinalOutput != "ok" {
		t.Errorf("RunStream = %q, %v; want ok after a retry (nothing was streamed)", s.FinalOutput, err)
	}
}
