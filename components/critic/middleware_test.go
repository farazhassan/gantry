package critic_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithCriticRejectionLoops(t *testing.T) {
	// Main LLM script: turn 1 returns "bad", turn 2 returns "good".
	mainLLM := eval.NewMockLLMClient(
		harness.LLMResponse{Content: "bad", StopReason: harness.StopReasonEnd},
		harness.LLMResponse{Content: "good", StopReason: harness.StopReasonEnd},
	)
	// Critic LLM: rejects "bad", accepts "good".
	criticLLM := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: harness.LLMResponse{Content: "FAIL: try again"}},
		{Response: harness.LLMResponse{Content: "PASS"}},
	})

	a, _ := harness.NewAgent(harness.WithLLM(mainLLM), harness.WithMaxIterations(5))
	critic.WithCritic(a, critic.NewLLM(criticLLM, ""))

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "good" {
		t.Errorf("FinalOutput = %q, want good", state.FinalOutput)
	}
	if state.Iteration != 2 {
		t.Errorf("Iteration = %d, want 2", state.Iteration)
	}
}

func TestWithCriticModifyOutputRewrites(t *testing.T) {
	mainLLM := eval.NewMockLLMClient(harness.LLMResponse{Content: "raw", StopReason: harness.StopReasonEnd})
	criticImpl := rewriteCritic{newOutput: "polished"}

	a, _ := harness.NewAgent(harness.WithLLM(mainLLM))
	critic.WithCritic(a, criticImpl)

	state, err := a.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if state.FinalOutput != "polished" {
		t.Errorf("FinalOutput = %q, want polished", state.FinalOutput)
	}
}

type rewriteCritic struct{ newOutput string }

func (r rewriteCritic) Critique(ctx context.Context, s *harness.State) (critic.Verdict, error) {
	return critic.Verdict{Accept: true, ModifyOutput: r.newOutput}, nil
}
