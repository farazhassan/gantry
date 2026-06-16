package eval_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func makeMockFactory(t *testing.T, replies ...gantry.LLMResponse) eval.AgentFactory {
	t.Helper()
	return func(ctx context.Context) (*gantry.Agent, error) {
		return gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(replies...)))
	}
}

func TestRunnerSingleConfigSingleCase(t *testing.T) {
	runner := eval.Runner{
		Configs: []eval.Config{{Name: "cfg", Factory: makeMockFactory(t, gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd})}},
		Dataset: eval.SliceDataset{{ID: "c1", Input: "hello"}},
		Scorers: []eval.Scorer{eval.ContainsScorer{Required: []string{"hi"}}},
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 1 {
		t.Fatalf("Results len = %d, want 1", len(report.Results))
	}
	if !report.Results[0].Scores[0].Pass {
		t.Errorf("expected contains scorer to pass; got %+v", report.Results[0].Scores)
	}
}

func TestRunnerMultipleConfigsAndCases(t *testing.T) {
	runner := eval.Runner{
		Configs: []eval.Config{
			{Name: "cfg-a", Factory: makeMockFactory(t,
				gantry.LLMResponse{Content: "first", StopReason: gantry.StopReasonEnd},
			)},
			{Name: "cfg-b", Factory: makeMockFactory(t,
				gantry.LLMResponse{Content: "second", StopReason: gantry.StopReasonEnd},
			)},
		},
		Dataset: eval.SliceDataset{
			{ID: "c1", Input: "x"},
			{ID: "c2", Input: "y"},
		},
		Scorers: []eval.Scorer{},
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 4 {
		t.Fatalf("Results len = %d, want 4 (2 configs × 2 cases)", len(report.Results))
	}
}

func TestRunnerCapturesRunError(t *testing.T) {
	failingFactory := func(ctx context.Context) (*gantry.Agent, error) {
		// MockLLMClient with empty script → first call returns ErrMockExhausted.
		return gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient()))
	}
	runner := eval.Runner{
		Configs: []eval.Config{{Name: "fail", Factory: failingFactory}},
		Dataset: eval.SliceDataset{{ID: "c1", Input: "x"}},
		Scorers: nil,
	}
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Runner.Run should not error on per-case failures; got %v", err)
	}
	if report.Results[0].Err == nil {
		t.Errorf("expected per-case Err, got nil")
	}
}
