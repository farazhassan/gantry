package eval_test

import (
	"context"
	"testing"
	"time"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestTraceScorerPredicate(t *testing.T) {
	tr := harness.NewTrace()
	tr.Record(harness.TraceEvent{Name: "phase:tool_exec", Kind: harness.KindSpanStart})

	s := eval.TraceScorer{
		ScorerName: "uses_tool_exec",
		Pred: func(t *harness.Trace) bool {
			for _, e := range t.Snapshot() {
				if e.Name == "phase:tool_exec" {
					return true
				}
			}
			return false
		},
	}
	score, err := s.Score(context.Background(), eval.Case{}, eval.RunResult{Trace: tr})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
}

func TestUsageScorerWithinBudget(t *testing.T) {
	s := eval.UsageScorer{MaxTokens: 1000, MaxCost: 0.50}
	r := eval.RunResult{Usage: harness.Usage{InputTokens: 100, OutputTokens: 50, Cost: 0.10}}
	score, _ := s.Score(context.Background(), eval.Case{}, r)
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
}

func TestUsageScorerOverTokens(t *testing.T) {
	s := eval.UsageScorer{MaxTokens: 100}
	r := eval.RunResult{Usage: harness.Usage{InputTokens: 200, OutputTokens: 50}}
	score, _ := s.Score(context.Background(), eval.Case{}, r)
	if score.Pass {
		t.Errorf("expected Fail; got %+v", score)
	}
}

func TestLatencyScorerUnder(t *testing.T) {
	s := eval.LatencyScorer{Max: 500 * time.Millisecond}
	r := eval.RunResult{Duration: 100 * time.Millisecond}
	score, _ := s.Score(context.Background(), eval.Case{}, r)
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
}

func TestLatencyScorerOver(t *testing.T) {
	s := eval.LatencyScorer{Max: 50 * time.Millisecond}
	r := eval.RunResult{Duration: 100 * time.Millisecond}
	score, _ := s.Score(context.Background(), eval.Case{}, r)
	if score.Pass {
		t.Errorf("expected Fail; got %+v", score)
	}
}
