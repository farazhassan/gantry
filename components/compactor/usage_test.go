package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
)

func TestSummarizerUsageCountsTowardRunAndLimiter(t *testing.T) {
	agentLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 5, OutputTokens: 5}})
	summarizer := eval.NewMockLLMClient(gantry.LLMResponse{Content: "S", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 50, OutputTokens: 10}})
	a, _ := gantry.NewAgent(gantry.WithLLM(agentLLM), gantry.WithContextWindow(2000))
	seed(t, a, []gantry.Message{user(xs(800)), assistant(xs(800)), user("q2")}) // 1602 ≥ 1600
	b := limiter.NewBudget(limiter.Limits{MaxTokens: 1_000_000})
	_ = a.With(limiter.New(b))
	p := compactor.NewPolicy(compactor.Policy{Steps: []compactor.Compactor{compactor.SummarizeTurns(summarizer, 1, compactor.WithSummaryMaxTokens(50))}})
	_ = a.With(compactor.New(p, compactor.Budget{Counter: lenCount}))

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 55, OutputTokens: 15}
	if s.Usage != want || b.Total() != want {
		t.Errorf("state Usage = %+v, limiter Total = %+v, want %+v each", s.Usage, b.Total(), want)
	}
	r, _ := s.Meta[compactor.MetaLastCompaction].(*compactor.Report)
	if r == nil || r.Usage != (gantry.Usage{InputTokens: 50, OutputTokens: 10}) {
		t.Errorf("report = %+v, want Usage 50/10", r)
	}
}

func TestSummarizingStrategyUsageCountsTowardRun(t *testing.T) {
	agentLLM := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	summarizer := eval.NewMockLLMClient(gantry.LLMResponse{Content: "S", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 7, OutputTokens: 3}})
	a, _ := gantry.NewAgent(gantry.WithLLM(agentLLM))
	seed(t, a, []gantry.Message{user("a"), assistant("b"), user("c")})
	_ = a.With(compactor.New(compactor.NewSummarizing(summarizer, 0, 1), compactor.Budget{}))

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := (gantry.Usage{InputTokens: 7, OutputTokens: 3}); s.Usage != want {
		t.Errorf("state Usage = %+v, want %+v", s.Usage, want)
	}
}
