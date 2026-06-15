package systemprompt_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/systemprompt"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

const persona = "You are a helpful assistant."

func newAgent(t *testing.T) (*harness.Agent, *eval.MockLLMClient) {
	t.Helper()
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, err := harness.New(harness.WithLLM(mock))
	if err != nil {
		t.Fatalf("harness.New: %v", err)
	}
	return a, mock
}

// TestSetsSystemWhenEmpty: a single-turn run starts at iteration 0 with an
// empty System, so the middleware sets the persona and it reaches the model.
func TestSetsSystemWhenEmpty(t *testing.T) {
	a, mock := newAgent(t)
	systemprompt.WithSystemPrompt(a, persona)

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mock.Requests()[0].System; got != persona {
		t.Fatalf("want System %q, got %q", persona, got)
	}
}

// TestDoesNotOverwriteExistingSystem: a pre-setter middleware registered AFTER
// WithSystemPrompt runs first (Compose is LIFO: last-registered executes
// outermost) and sets System; WithSystemPrompt must then leave it untouched
// because System is no longer empty when its handler fires.
func TestDoesNotOverwriteExistingSystem(t *testing.T) {
	a, mock := newAgent(t)
	systemprompt.WithSystemPrompt(a, persona)
	_ = a.UseNamed(harness.PhaseAssembleContext, "test/presetter", func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, state *harness.State) error {
			if state.System == "" {
				state.System = "PRESET"
			}
			return next(ctx, state)
		}
	})

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := mock.Requests()[0].System; got != "PRESET" {
		t.Fatalf("want System left as %q, got %q", "PRESET", got)
	}
}

// TestEmptyPromptRegistersNoMiddleware: a blank prompt installs nothing.
func TestEmptyPromptRegistersNoMiddleware(t *testing.T) {
	a, _ := newAgent(t)
	before := a.MiddlewareCount(harness.PhaseAssembleContext)
	systemprompt.WithSystemPrompt(a, "")
	if got := a.MiddlewareCount(harness.PhaseAssembleContext); got != before {
		t.Fatalf("empty prompt should register nothing: count %d -> %d", before, got)
	}
}

// TestRegistersExactlyOneMiddleware: a non-empty prompt installs exactly one
// middleware on the context-assembly phase.
func TestRegistersExactlyOneMiddleware(t *testing.T) {
	a, _ := newAgent(t)
	before := a.MiddlewareCount(harness.PhaseAssembleContext)
	systemprompt.WithSystemPrompt(a, persona)
	if got := a.MiddlewareCount(harness.PhaseAssembleContext); got != before+1 {
		t.Fatalf("want one middleware added: count %d -> %d", before, got)
	}
}
