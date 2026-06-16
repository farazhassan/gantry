package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestMiddlewareWrappingOrder(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.NewAgent(harness.WithLLM(mock))

	var log []string
	mk := func(name string) harness.Middleware {
		return func(next harness.Handler) harness.Handler {
			return func(ctx context.Context, s *harness.State) error {
				log = append(log, name+"-pre")
				err := next(ctx, s)
				log = append(log, name+"-post")
				return err
			}
		}
	}

	a.Use(harness.PhaseLLMCall, mk("A"))
	a.Use(harness.PhaseLLMCall, mk("B"))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Registration order: [A, B] → B is outer wrap.
	// Pre order: B-pre, A-pre. Post: A-post, B-post.
	want := []string{"B-pre", "A-pre", "A-post", "B-post"}
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Errorf("log[%d] = %q, want %q", i, log[i], want[i])
		}
	}
}
