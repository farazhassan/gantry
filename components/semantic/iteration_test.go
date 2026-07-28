package semantic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/semantic"
	"github.com/farazhassan/gantry/eval"
)

// TestMultiIterationRunRecallsOnceAndPersistsOnce guards the two iteration
// guards: recall must not re-append the memories block on later iterations
// (state.System persists across iterations), and persist must only store the
// final turn pair, not one pair per iteration.
func TestMultiIterationRunRecallsOnceAndPersistsOnce(t *testing.T) {
	store := semantic.NewInMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, semantic.Item{Text: "seeded memory", Vector: []float32{1, 0}})
	emb := &stubEmbedder{}

	a, mock := newAgent(t, semantic.New(store, emb),
		gantry.LLMResponse{ToolCalls: []gantry.ToolCall{{ID: "t1", Name: "x"}}, StopReason: gantry.StopReasonToolUse},
		gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd},
	)

	if _, err := a.Run(ctx, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	// The block must appear exactly once in the second request's System.
	if n := strings.Count(reqs[1].System, "Relevant memories"); n != 1 {
		t.Errorf("second request has %d memory blocks, want 1; System: %q", n, reqs[1].System)
	}
	// One recall embed + one persist embed, nothing per-iteration.
	if len(emb.calls) != 2 {
		t.Errorf("Embed called %d times, want 2; calls: %v", len(emb.calls), emb.calls)
	}
	// Seeded memory + user + assistant = 3 items; not one pair per iteration.
	hits, err := store.Search(ctx, []float32{1, 0}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 3 {
		t.Errorf("store has %d items, want 3; %+v", len(hits), hits)
	}
}

// TestPersistRequiresDoneAlongsideFinalOutput guards the Done half of the
// persist guard (s.Done && s.FinalOutput != "").
//
// A max-iterations-exhaustion run (the LLM only ever returns tool calls, so
// the loop exhausts instead of finishing cleanly) does NOT exercise this half
// of the guard: DefaultPostLLMHandler only sets FinalOutput when a response
// carries no tool calls, and DoneMaxIterations is assigned only after the
// loop exits -- so in that scenario FinalOutput stays "" the whole run and
// both `s.Done && s.FinalOutput != ""` and a hypothetical `s.FinalOutput !=
// ""`-only guard agree (skip persist) for the same reason. Likewise, the
// critic component (see components/critic/middleware.go) always sets or
// clears Done and FinalOutput together, never one without the other. So no
// built-in component ever produces a reachable state where FinalOutput != ""
// and Done == false -- confirmed by first writing this test against a
// tool-call-exhaustion run and observing it stayed green even with `s.Done
// &&` removed from the guard.
//
// To actually pin the Done conjunct, this test uses a probe middleware
// registered inner to semantic's persist (i.e. before semantic.New, so
// persist's post-next check observes the probe's effect): it lets the
// default handler finish the turn normally (Done=true, FinalOutput set) and
// then clears Done, simulating a component that leaves FinalOutput populated
// without a clean finish. persist must still refuse to store.
func TestPersistRequiresDoneAlongsideFinalOutput(t *testing.T) {
	store := semantic.NewInMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx, semantic.Item{Text: "seeded memory", Vector: []float32{1, 0}})
	emb := &stubEmbedder{}

	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "final", StopReason: gantry.StopReasonEnd})
	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(1))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	// Registered before semantic.New so it is inner to persist: registration
	// order is innermost-first (see gantry.Compose), so persist's check runs
	// after this probe's post-next code has already cleared Done.
	if err := a.UseNamed(gantry.PhasePostLLM, "test:clear-done-after-finish", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if err := next(ctx, s); err != nil {
				return err
			}
			s.Done = false
			return nil
		}
	}); err != nil {
		t.Fatalf("install probe: %v", err)
	}
	if err := a.With(semantic.New(store, emb)); err != nil {
		t.Fatalf("install semantic: %v", err)
	}

	if _, err := a.Run(ctx, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	hits, err := store.Search(ctx, []float32{1, 0}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Errorf("store has %d items, want 1 (only the seeded memory; persist must not fire on FinalOutput alone); %+v", len(hits), hits)
	}
	if len(emb.calls) != 1 {
		t.Errorf("Embed called %d times, want 1 (recall only, no persist); calls: %v", len(emb.calls), emb.calls)
	}
}
