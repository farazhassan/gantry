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
