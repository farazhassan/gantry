package eval_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestLLMJudgePassesOnApprove(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "VERDICT: PASS\nScore: 0.95\nReason: good"})
	s := eval.LLMJudgeScorer{Client: mock, Rubric: "Reply VERDICT: PASS or FAIL with a Score."}

	score, err := s.Score(context.Background(), eval.Case{Input: "q"}, eval.RunResult{FinalOutput: "a"})
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if !score.Pass {
		t.Errorf("expected Pass; got %+v", score)
	}
}

func TestLLMJudgeFailsOnReject(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "VERDICT: FAIL\nReason: incomplete"})
	s := eval.LLMJudgeScorer{Client: mock, Rubric: "x"}
	score, _ := s.Score(context.Background(), eval.Case{}, eval.RunResult{FinalOutput: "ok"})
	if score.Pass {
		t.Errorf("expected Fail; got %+v", score)
	}
}
