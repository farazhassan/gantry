package gantry_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/farazhassan/gantry"
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
