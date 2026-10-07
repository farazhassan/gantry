package compactor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

// recordingCompactor keeps the last n messages and records every Budget it saw.
type recordingCompactor struct {
	keep    int
	budgets []compactor.Budget
}

func (r *recordingCompactor) Compact(_ context.Context, msgs []gantry.Message, b compactor.Budget) ([]gantry.Message, error) {
	r.budgets = append(r.budgets, b)
	start := 0
	if len(msgs) > r.keep {
		start = len(msgs) - r.keep
	}
	out := make([]gantry.Message, len(msgs)-start)
	copy(out, msgs[start:])
	return out, nil
}

func preload(t *testing.T, a *gantry.Agent, n int) {
	t.Helper()
	err := a.UseNamed(gantry.PhaseAssembleContext, "preload", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if len(s.Messages) < n {
				s.Messages = nil
				for i := 0; i < n; i++ {
					s.Messages = append(s.Messages, gantry.Message{Role: gantry.RoleUser, Content: "msg"})
				}
			}
			return next(ctx, s)
		}
	})
	if err != nil {
		t.Fatalf("preload: %v", err)
	}
}

func overflow(limit int) error {
	return &gantry.ContextLengthError{Limit: limit, Requested: limit + 1, Err: errors.New("too long")}
}

func TestOverflowRetriesOnceAfterForcedCompaction(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 6)
	rc := &recordingCompactor{keep: 100} // assemble pass keeps all 6
	if err := a.With(compactor.New(rc, compactor.Budget{SoftLimit: 50})); err != nil {
		t.Fatalf("With: %v", err)
	}

	s, err := a.Run(context.Background(), "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(mock.Requests()); got != 2 {
		t.Fatalf("LLM calls = %d, want 2", got)
	}
	last := rc.budgets[len(rc.budgets)-1]
	if !last.Force || last.MaxTokens != 300 || last.SoftLimit != 50 {
		t.Errorf("retry budget = %+v, want Force, MaxTokens 300, SoftLimit 50", last)
	}
	if got, _ := s.Meta[compactor.MetaOverflowRetries].(int); got != 1 {
		t.Errorf("Meta[%s] = %v, want 1", compactor.MetaOverflowRetries, s.Meta[compactor.MetaOverflowRetries])
	}
}

func TestOverflowTargetFallsBackToContextWindow(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: &gantry.ContextLengthError{Err: errors.New("too long")}}, // no Limit
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithContextWindow(800))
	preload(t, a, 3)
	rc := &recordingCompactor{keep: 100}
	_ = a.With(compactor.New(rc, compactor.Budget{}))

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 800 {
		t.Errorf("retry MaxTokens = %d, want 800 (ContextWindow)", got)
	}
}

func TestOverflowTargetFallsBackToEstimate(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: &gantry.ContextLengthError{Err: errors.New("too long")}},
		{Response: gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 4)
	rc := &recordingCompactor{keep: 100}
	_ = a.With(compactor.New(rc, compactor.Budget{Counter: func(gantry.Message) int { return 100 }}))

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No anchor, no system/tools: estimate = 4×100 = 400; 75% = 300.
	if got := rc.budgets[len(rc.budgets)-1].MaxTokens; got != 300 {
		t.Errorf("retry MaxTokens = %d, want 300 (75%% of estimate)", got)
	}
}

func TestOverflowTwiceReturnsTypedError(t *testing.T) {
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{
		{Err: overflow(300)},
		{Err: overflow(300)},
	})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 3)
	_ = a.With(compactor.New(&recordingCompactor{keep: 100}, compactor.Budget{}))

	_, err := a.Run(context.Background(), "")
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Fatalf("Run err = %v, want ErrContextLengthExceeded", err)
	}
	if got := len(mock.Requests()); got != 2 {
		t.Errorf("LLM calls = %d, want 2 (one retry only)", got)
	}
}

func TestNonOverflowErrorIsNotRetried(t *testing.T) {
	boom := errors.New("boom")
	mock := eval.NewMockLLMClientFromScript([]eval.MockTurn{{Err: boom}})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	_ = a.With(compactor.New(&recordingCompactor{keep: 100}, compactor.Budget{}))

	_, err := a.Run(context.Background(), "hi")
	if !errors.Is(err, boom) {
		t.Fatalf("Run err = %v, want boom", err)
	}
	if got := len(mock.Requests()); got != 1 {
		t.Errorf("LLM calls = %d, want 1", got)
	}
}

func TestAssembleResetsAnchorWhenCompactionShrinks(t *testing.T) {
	var seen []gantry.ContextUsage
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	preload(t, a, 5)
	_ = a.UseNamed(gantry.PhaseAssembleContext, "seed-anchor", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.ContextUsage = gantry.ContextUsage{PromptTokens: 900, MessageCount: 4}
			return next(ctx, s)
		}
	})
	_ = a.With(compactor.New(&recordingCompactor{keep: 2}, compactor.Budget{}))
	a.Use(gantry.PhaseLLMCall, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			seen = append(seen, s.ContextUsage)
			return next(ctx, s)
		}
	})

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen[0] != (gantry.ContextUsage{}) {
		t.Errorf("anchor before LLM call = %+v, want reset after shrinking compaction", seen[0])
	}
}
