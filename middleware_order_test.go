package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func TestMiddlewareWrappingOrder(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))

	var log []string
	mk := func(name string) gantry.Middleware {
		return func(next gantry.Handler) gantry.Handler {
			return func(ctx context.Context, s *gantry.State) error {
				log = append(log, name+"-pre")
				err := next(ctx, s)
				log = append(log, name+"-post")
				return err
			}
		}
	}

	a.Use(gantry.PhaseLLMCall, mk("A"))
	a.Use(gantry.PhaseLLMCall, mk("B"))

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
