package limiter_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/limiter"
	"github.com/farazhassan/gantry/eval"
)

// spendOnAssemble adds u to State.Usage on the first context assembly, as a
// compactor's summarizer call does.
func spendOnAssemble(a *gantry.Agent, u gantry.Usage) {
	done := false
	_ = a.UseNamed(gantry.PhaseAssembleContext, "spend", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if !done {
				done = true
				s.Usage = s.Usage.Add(u)
			}
			return next(ctx, s)
		}
	})
}

func TestLimiterRecordsUsageAddedOutsideLLMCalls(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 5, OutputTokens: 5}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	spendOnAssemble(a, gantry.Usage{InputTokens: 40})
	b := limiter.NewBudget(limiter.Limits{MaxTokens: 1_000_000})
	_ = a.With(limiter.New(b))

	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 45, OutputTokens: 5}
	if got := b.Total(); got != want || s.Usage != want {
		t.Errorf("limiter Total = %+v, state Usage = %+v, want %+v each", got, s.Usage, want)
	}
}

func TestLimiterRecordsUsageSpentByOverflowHandler(t *testing.T) {
	cut := gantry.LLMResponse{
		ToolCalls:  []gantry.ToolCall{{ID: "t1", Name: "noop", Input: []byte(`{"q":`)}},
		StopReason: gantry.StopReasonContextWindow,
		Usage:      gantry.Usage{InputTokens: 90, OutputTokens: 10},
	}
	ok := gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd, Usage: gantry.Usage{InputTokens: 5, OutputTokens: 5}}
	a, _ := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(cut, ok)))
	b := limiter.NewBudget(limiter.Limits{MaxTokens: 1_000_000})
	_ = a.With(limiter.New(b))
	_ = a.OnContextOverflow(func(_ context.Context, s *gantry.State, _ error) (bool, error) {
		s.Usage = s.Usage.Add(gantry.Usage{InputTokens: 20}) // e.g. a summarizer call
		return true, nil
	})

	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := gantry.Usage{InputTokens: 115, OutputTokens: 15}
	if got := b.Total(); got != want {
		t.Errorf("limiter Total = %+v, want %+v", got, want)
	}
}

func TestLimiterStopsRunWhenOutOfBandUsageExceedsBudget(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	spendOnAssemble(a, gantry.Usage{InputTokens: 150})
	_ = a.With(limiter.New(limiter.NewBudget(limiter.Limits{MaxTokens: 100})))

	s, err := a.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s.DoneReason != gantry.DoneBudgetExceeded || len(mock.Requests()) != 0 {
		t.Errorf("DoneReason = %q, LLM calls = %d; want %q and no call", s.DoneReason, len(mock.Requests()), gantry.DoneBudgetExceeded)
	}
}
