package eval_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestAgentFactoryProducesAgent(t *testing.T) {
	factory := func(ctx context.Context) (*harness.Agent, error) {
		return harness.NewAgent(harness.WithLLM(eval.NewMockLLMClient()))
	}
	cfg := eval.Config{Name: "test", Factory: factory}
	a, err := cfg.Factory(context.Background())
	if err != nil {
		t.Fatalf("Factory: %v", err)
	}
	if a == nil {
		t.Errorf("agent is nil")
	}
}
