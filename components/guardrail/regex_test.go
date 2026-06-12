package guardrail_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry/components/guardrail"
	"github.com/farazhassan/gantry/harness"
)

func TestRegexGuardrailBlocksInputMatchingPattern(t *testing.T) {
	g := guardrail.NewRegex(`(?i)password`, guardrail.DirectionInput)
	state := &harness.State{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "what is my password"}},
	}
	err := g.Check(context.Background(), state, guardrail.DirectionInput)
	if !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
}

func TestRegexGuardrailPassesClean(t *testing.T) {
	g := guardrail.NewRegex(`(?i)password`, guardrail.DirectionInput)
	state := &harness.State{
		Messages: []harness.Message{{Role: harness.RoleUser, Content: "what is the weather"}},
	}
	if err := g.Check(context.Background(), state, guardrail.DirectionInput); err != nil {
		t.Errorf("expected pass; got %v", err)
	}
}

func TestRegexGuardrailChecksOutput(t *testing.T) {
	g := guardrail.NewRegex(`(?i)secret`, guardrail.DirectionOutput)
	state := &harness.State{
		LastResponse: &harness.LLMResponse{Content: "the secret is 42"},
	}
	err := g.Check(context.Background(), state, guardrail.DirectionOutput)
	if !errors.Is(err, harness.ErrGuardrailBlocked) {
		t.Errorf("expected ErrGuardrailBlocked; got %v", err)
	}
}

func TestRegexGuardrailDirectionMismatchSkips(t *testing.T) {
	// Configured for input; called with output direction → should pass.
	g := guardrail.NewRegex(`(?i)password`, guardrail.DirectionInput)
	state := &harness.State{
		LastResponse: &harness.LLMResponse{Content: "your password is 12345"},
	}
	if err := g.Check(context.Background(), state, guardrail.DirectionOutput); err != nil {
		t.Errorf("expected pass on wrong direction; got %v", err)
	}
}
