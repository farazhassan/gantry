package eval_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/memory"
	"github.com/farazhassan/gantry/eval"
)

func TestEvalE2ESweepAcrossConfigs(t *testing.T) {
	dataset := eval.SliceDataset{
		{ID: "greet", Input: "say hi", Metadata: map[string]any{"expected": "hello"}},
		{ID: "farewell", Input: "say bye", Metadata: map[string]any{"expected": "goodbye"}},
	}

	mkConfig := func(name, reply string) eval.Config {
		return eval.Config{
			Name: name,
			Factory: func(ctx context.Context) (*gantry.Agent, error) {
				mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: reply, StopReason: gantry.StopReasonEnd})
				a, err := gantry.NewAgent(gantry.WithLLM(mock))
				if err != nil {
					return nil, err
				}
				if err := a.With(memory.New(memory.NewInMemoryStore())); err != nil {
					return nil, err
				}
				return a, nil
			},
		}
	}

	runner := eval.Runner{
		Configs: []eval.Config{
			mkConfig("always-hello", "hello"),
			mkConfig("always-goodbye", "goodbye"),
		},
		Dataset:     dataset,
		Scorers:     []eval.Scorer{eval.ExactMatchScorer{ExpectedKey: "expected"}},
		Parallelism: 4,
	}

	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Results) != 4 {
		t.Fatalf("results = %d, want 4", len(report.Results))
	}
	// "always-hello" matches case "greet" (expected: hello), fails "farewell".
	// "always-goodbye" matches "farewell", fails "greet".
	helloSummary := report.Summary["always-hello"]
	if helloSummary.PassRate["exact_match"] != 0.5 {
		t.Errorf("always-hello pass rate = %v, want 0.5", helloSummary.PassRate["exact_match"])
	}
	byeSummary := report.Summary["always-goodbye"]
	if byeSummary.PassRate["exact_match"] != 0.5 {
		t.Errorf("always-goodbye pass rate = %v, want 0.5", byeSummary.PassRate["exact_match"])
	}

	var sb strings.Builder
	if err := report.WriteSummary(&sb); err != nil {
		t.Fatalf("WriteSummary: %v", err)
	}
	if !strings.Contains(sb.String(), "always-hello") {
		t.Errorf("summary missing config name")
	}
}
