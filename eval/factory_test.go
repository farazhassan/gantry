package eval_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestAgentFactoryProducesAgent(t *testing.T) {
	factory := func(ctx context.Context) (*gantry.Agent, error) {
		return gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
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
