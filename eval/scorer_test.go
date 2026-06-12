package eval_test

import (
	"testing"

	"github.com/farazhassan/gantry/eval"
)

func TestScoreZeroValue(t *testing.T) {
	var s eval.Score
	if s.Pass || s.Value != 0 {
		t.Errorf("zero Score should be Pass=false, Value=0; got %+v", s)
	}
}

func TestScoredResultEmbeds(t *testing.T) {
	r := eval.ScoredResult{
		RunResult: eval.RunResult{FinalOutput: "x"},
		Scores:    []eval.Score{{Name: "a", Pass: true, Value: 1}},
	}
	if r.FinalOutput != "x" {
		t.Errorf("FinalOutput = %q", r.FinalOutput)
	}
	if len(r.Scores) != 1 || !r.Scores[0].Pass {
		t.Errorf("scores = %+v", r.Scores)
	}
}
