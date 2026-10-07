package gantry_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestUsageAddSumsCacheFields(t *testing.T) {
	a := gantry.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 6, CacheWriteTokens: 1, Cost: 0.5}
	b := gantry.Usage{InputTokens: 5, OutputTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 2, Cost: 0.25}
	got := a.Add(b)
	want := gantry.Usage{InputTokens: 15, OutputTokens: 5, CacheReadTokens: 10, CacheWriteTokens: 3, Cost: 0.75}
	if got != want {
		t.Errorf("Add = %+v, want %+v", got, want)
	}
}

func TestContextLengthErrorMatchesSentinel(t *testing.T) {
	provider := errors.New("anthropic: messages: status 400: prompt is too long")
	var err error = &gantry.ContextLengthError{Limit: 200000, Requested: 210000, Err: provider}
	wrapped := fmt.Errorf("phase llm_call: %w", err)

	if !errors.Is(wrapped, gantry.ErrContextLengthExceeded) {
		t.Error("errors.Is(wrapped, ErrContextLengthExceeded) = false, want true")
	}
	if !errors.Is(wrapped, provider) {
		t.Error("errors.Is(wrapped, provider) = false, want true (Unwrap)")
	}
	var cle *gantry.ContextLengthError
	if !errors.As(wrapped, &cle) || cle.Limit != 200000 || cle.Requested != 210000 {
		t.Errorf("errors.As = %+v, want Limit 200000 Requested 210000", cle)
	}
	if got := err.Error(); got != "gantry: context length exceeded (requested 210000, limit 200000): anthropic: messages: status 400: prompt is too long" {
		t.Errorf("Error() = %q", got)
	}
}

func TestContextLengthErrorMessageWithoutNumbers(t *testing.T) {
	err := &gantry.ContextLengthError{Err: errors.New("boom")}
	if got := err.Error(); got != "gantry: context length exceeded: boom" {
		t.Errorf("Error() = %q", got)
	}
}

func TestStopReasonContextWindowValue(t *testing.T) {
	if gantry.StopReasonContextWindow != "context_window" {
		t.Errorf("StopReasonContextWindow = %q", gantry.StopReasonContextWindow)
	}
}

// windowLLM wraps the mock and reports a fixed context window (or an error).
type windowLLM struct {
	*eval.MockLLMClient
	window int
	err    error
	calls  int
}

func (w *windowLLM) ContextWindow(ctx context.Context) (int, error) {
	w.calls++
	return w.window, w.err
}

func endResp() gantry.LLMResponse {
	return gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}
}

func TestContextWindowFromReporter(t *testing.T) {
	llm := &windowLLM{MockLLMClient: eval.NewMockLLMClient(endResp()), window: 1000}
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ContextWindow != 1000 {
		t.Errorf("ContextWindow = %d, want 1000", s.ContextWindow)
	}
}

func TestContextWindowOptionOverridesReporter(t *testing.T) {
	llm := &windowLLM{MockLLMClient: eval.NewMockLLMClient(endResp()), window: 1000}
	a, _ := gantry.NewAgent(gantry.WithLLM(llm), gantry.WithContextWindow(500))
	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ContextWindow != 500 {
		t.Errorf("ContextWindow = %d, want 500", s.ContextWindow)
	}
	if llm.calls != 0 {
		t.Errorf("reporter called %d times, want 0 when option set", llm.calls)
	}
}

func TestContextWindowReporterErrorDoesNotFailRun(t *testing.T) {
	llm := &windowLLM{MockLLMClient: eval.NewMockLLMClient(endResp()), err: errors.New("lookup failed")}
	a, _ := gantry.NewAgent(gantry.WithLLM(llm))
	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v (reporter error must not fail the run)", err)
	}
	if s.ContextWindow != 0 {
		t.Errorf("ContextWindow = %d, want 0 (unknown)", s.ContextWindow)
	}
}

func TestContextWindowUnknownWithoutReporter(t *testing.T) {
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(endResp())))
	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.ContextWindow != 0 {
		t.Errorf("ContextWindow = %d, want 0", s.ContextWindow)
	}
}

func TestWithContextWindowRejectsNegative(t *testing.T) {
	if _, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()), gantry.WithContextWindow(-1)); err == nil {
		t.Error("WithContextWindow(-1): want error")
	}
}

func TestContextUsageAnchoredAfterCall(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{
		Content: "ok", StopReason: gantry.StopReasonEnd,
		Usage: gantry.Usage{InputTokens: 120, OutputTokens: 30},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.ContextUsage{PromptTokens: 150, MessageCount: 1}
	if s.ContextUsage != want {
		t.Errorf("ContextUsage = %+v, want %+v", s.ContextUsage, want)
	}
}

func TestContextUsageClearedWhenUsageMissing(t *testing.T) {
	mock := eval.NewMockLLMClient(endResp()) // no Usage
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	s := gantry.NewState("hi")
	s.ContextUsage = gantry.ContextUsage{PromptTokens: 99, MessageCount: 7}
	s, err := a.Resume(context.Background(), s)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if s.ContextUsage != (gantry.ContextUsage{}) {
		t.Errorf("ContextUsage = %+v, want zero", s.ContextUsage)
	}
}

func TestRunFromCarriesContextUsageAndWindow(t *testing.T) {
	prior := gantry.NewState("hi")
	prior.Messages = []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}, {Role: gantry.RoleAssistant, Content: "ok"}}
	prior.ContextUsage = gantry.ContextUsage{PromptTokens: 150, MessageCount: 1}
	prior.ContextWindow = 4096
	prior.Done = true

	var seen gantry.ContextUsage
	var seenWindow int
	mock := eval.NewMockLLMClient(endResp())
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			seen, seenWindow = s.ContextUsage, s.ContextWindow
			return next(ctx, s)
		}
	})
	if _, err := a.RunFrom(context.Background(), prior, "next"); err != nil {
		t.Fatalf("RunFrom: %v", err)
	}
	if seen != prior.ContextUsage || seenWindow != 4096 {
		t.Errorf("next turn saw ContextUsage %+v window %d, want %+v and 4096", seen, seenWindow, prior.ContextUsage)
	}
}
