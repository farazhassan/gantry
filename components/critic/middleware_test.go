package critic_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/critic"
	"github.com/farazhassan/gantry/eval"
)

func TestWithCriticRejectionLoops(t *testing.T) {
	// Main LLM script: turn 1 returns "bad", turn 2 returns "good".
	mainLLM := eval.NewMockLLMClient(
		gantry.LLMResponse{Content: "bad", StopReason: gantry.StopReasonEnd},
		gantry.LLMResponse{Content: "good", StopReason: gantry.StopReasonEnd},
	)
	// Critic LLM: rejects "bad", accepts "good".
	criticLLM := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Response: gantry.LLMResponse{Content: "FAIL: try again"}},
		{Response: gantry.LLMResponse{Content: "PASS"}},
	})

	a, _ := gantry.NewAgent(gantry.WithLLM(mainLLM), gantry.WithMaxIterations(5))
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
	mainLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "raw", StopReason: gantry.StopReasonEnd})
	criticImpl := rewriteCritic{newOutput: "polished"}

	a, _ := gantry.NewAgent(gantry.WithLLM(mainLLM))
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

func (r rewriteCritic) Critique(ctx context.Context, s *gantry.State) (critic.Verdict, error) {
	return critic.Verdict{Accept: true, ModifyOutput: r.newOutput}, nil
}
