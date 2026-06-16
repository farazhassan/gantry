package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

type nilLLM struct{}

func (nilLLM) Generate(ctx context.Context, req harness.LLMRequest) (harness.LLMResponse, error) {
	return harness.LLMResponse{}, nil
}

func TestNewAgentRequiresLLM(t *testing.T) {
	a, err := harness.NewAgent()
	if err == nil {
		t.Errorf("New() without LLM should error; got agent %v", a)
	}
}

func TestNewAgentWithLLM(t *testing.T) {
	a, err := harness.NewAgent(harness.WithLLM(nilLLM{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a == nil {
		t.Fatalf("agent is nil")
	}
}

func TestAgentDefaultMaxIterations(t *testing.T) {
	a, err := harness.NewAgent(harness.WithLLM(nilLLM{}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.MaxIterations() <= 0 {
		t.Errorf("default MaxIterations = %d, want > 0", a.MaxIterations())
	}
}

func TestAgentUseRegistersMiddleware(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	a.Use(harness.PhaseLLMCall, mw)
	if got := a.MiddlewareCount(harness.PhaseLLMCall); got != 1 {
		t.Errorf("MiddlewareCount = %d, want 1", got)
	}
	if got := a.MiddlewareCount(harness.PhaseObserve); got != 0 {
		t.Errorf("unrelated phase count = %d, want 0", got)
	}
}

func TestWithMaxIterations(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}), harness.WithMaxIterations(42))
	if a.MaxIterations() != 42 {
		t.Errorf("MaxIterations = %d, want 42", a.MaxIterations())
	}
}

func TestWithTracer(t *testing.T) {
	tr := harness.NewTrace()
	a, err := harness.NewAgent(harness.WithLLM(nilLLM{}), harness.WithTracer(harness.NewDefaultTracer(tr)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Tracer() == nil {
		t.Errorf("Tracer() is nil")
	}
}

func TestUseAppendsInOrder(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mk := func() harness.Middleware {
		return func(next harness.Handler) harness.Handler { return next }
	}
	a.Use(harness.PhaseLLMCall, mk())
	a.Use(harness.PhaseLLMCall, mk())
	a.Use(harness.PhaseLLMCall, mk())
	if got := a.MiddlewareCount(harness.PhaseLLMCall); got != 3 {
		t.Fatalf("MiddlewareCount = %d, want 3", got)
	}
}

func TestWithLLMNilErrors(t *testing.T) {
	_, err := harness.NewAgent(harness.WithLLM(nil))
	if err == nil {
		t.Errorf("expected error for nil LLM")
	}
}

func TestWithMaxIterationsZeroErrors(t *testing.T) {
	_, err := harness.NewAgent(harness.WithLLM(nilLLM{}), harness.WithMaxIterations(0))
	if err == nil {
		t.Errorf("expected error for MaxIterations(0)")
	}
}

func TestWithMaxIterationsNegativeErrors(t *testing.T) {
	_, err := harness.NewAgent(harness.WithLLM(nilLLM{}), harness.WithMaxIterations(-5))
	if err == nil {
		t.Errorf("expected error for MaxIterations(-5)")
	}
}

func TestUseNamedRegistersWithName(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	a.UseNamed(harness.PhaseLLMCall, "logger", mw)
	if got := a.MiddlewareCount(harness.PhaseLLMCall); got != 1 {
		t.Errorf("MiddlewareCount = %d, want 1", got)
	}
	names := a.MiddlewareNames(harness.PhaseLLMCall)
	if len(names) != 1 || names[0] != "logger" {
		t.Errorf("MiddlewareNames = %v, want [logger]", names)
	}
}

func TestUseBeforeInsertsBeforeAnchor(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	a.UseNamed(harness.PhaseLLMCall, "logger", mw)
	a.UseBefore(harness.PhaseLLMCall, "logger", "retry", mw)
	names := a.MiddlewareNames(harness.PhaseLLMCall)
	want := []string{"retry", "logger"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}

func TestUseAfterInsertsAfterAnchor(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	a.UseNamed(harness.PhaseLLMCall, "logger", mw)
	a.UseAfter(harness.PhaseLLMCall, "logger", "metrics", mw)
	names := a.MiddlewareNames(harness.PhaseLLMCall)
	want := []string{"logger", "metrics"}
	if len(names) != len(want) || names[0] != want[0] || names[1] != want[1] {
		t.Errorf("names = %v, want %v", names, want)
	}
}

func TestUseBeforeUnknownAnchorErrors(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	err := a.UseBefore(harness.PhaseLLMCall, "ghost", "metrics", mw)
	if err == nil {
		t.Errorf("UseBefore with unknown anchor should error; got nil")
	}
}

func TestUseStillAppendsAnonymously(t *testing.T) {
	a, _ := harness.NewAgent(harness.WithLLM(nilLLM{}))
	mw := func(next harness.Handler) harness.Handler { return next }
	a.Use(harness.PhaseLLMCall, mw)
	a.Use(harness.PhaseLLMCall, mw)
	names := a.MiddlewareNames(harness.PhaseLLMCall)
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	// Anonymous middleware should have auto-generated unique names.
	if names[0] == names[1] {
		t.Errorf("anonymous middleware names should be unique; got %v", names)
	}
}
