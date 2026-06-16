package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithCompactorTrimsBeforeLLMCall(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	// Preload some messages via middleware on PhaseAssembleContext.
	_ = a.UseNamed(harness.PhaseAssembleContext, "preload", func(next harness.Handler) harness.Handler {
		return func(ctx context.Context, s *harness.State) error {
			s.Messages = []harness.Message{
				{Role: harness.RoleUser, Content: "old1"},
				{Role: harness.RoleUser, Content: "old2"},
				{Role: harness.RoleUser, Content: "old3"},
				{Role: harness.RoleUser, Content: "old4"},
			}
			return next(ctx, s)
		}
	})

	compactor.WithCompactor(a, compactor.NewSlidingWindow(2), compactor.Budget{})

	if _, err := a.Run(context.Background(), ""); err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := mock.Requests()[0]
	if len(req.Messages) != 2 {
		t.Errorf("LLM saw %d messages; want 2 (compacted)", len(req.Messages))
	}
}
