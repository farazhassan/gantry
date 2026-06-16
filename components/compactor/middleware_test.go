package compactor_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/compactor"
	"github.com/farazhassan/gantry/eval"
)

func TestWithCompactorTrimsBeforeLLMCall(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	// Preload some messages via middleware on PhaseAssembleContext.
	_ = a.UseNamed(gantry.PhaseAssembleContext, "preload", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			s.Messages = []gantry.Message{
				{Role: gantry.RoleUser, Content: "old1"},
				{Role: gantry.RoleUser, Content: "old2"},
				{Role: gantry.RoleUser, Content: "old3"},
				{Role: gantry.RoleUser, Content: "old4"},
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
