package eval_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
)

func TestExactMatchScorerPass(t *testing.T) {
	s := eval.ExactMatchScorer{ExpectedKey: "expected"}
	c := eval.Case{ID: "c1", Input: "hi", Metadata: map[string]any{"expected": "hello"}}
	r := eval.RunResult{FinalOutput: "hello"}
	score, err := s.Score(context.Background(), c, r)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
	if score.Value != 1.0 {
		t.Errorf("Value = %v", score.Value)
	}
}

func TestExactMatchScorerFail(t *testing.T) {
	s := eval.ExactMatchScorer{ExpectedKey: "expected"}
	c := eval.Case{Metadata: map[string]any{"expected": "hello"}}
	r := eval.RunResult{FinalOutput: "hi"}
	score, _ := s.Score(context.Background(), c, r)
	if score.Pass {
		t.Errorf("expected Fail; got %+v", score)
	}
}

func TestExactMatchScorerMissingMetadataIsError(t *testing.T) {
	s := eval.ExactMatchScorer{ExpectedKey: "expected"}
	c := eval.Case{}
	r := eval.RunResult{}
	if _, err := s.Score(context.Background(), c, r); err == nil {
		t.Errorf("expected error for missing metadata")
	}
}

func TestRegexScorerMatchesPattern(t *testing.T) {
	s := eval.RegexScorer{Pattern: `[0-9]+`}
	r := eval.RunResult{FinalOutput: "answer is 42"}
	score, err := s.Score(context.Background(), eval.Case{}, r)
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
}

func TestContainsScorerAllRequired(t *testing.T) {
	s := eval.ContainsScorer{Required: []string{"foo", "bar"}}
	pass, _ := s.Score(context.Background(), eval.Case{}, eval.RunResult{FinalOutput: "foo and bar"})
	fail, _ := s.Score(context.Background(), eval.Case{}, eval.RunResult{FinalOutput: "only foo"})
	if !pass.Pass || fail.Pass {
		t.Errorf("pass=%+v fail=%+v", pass, fail)
	}
}
