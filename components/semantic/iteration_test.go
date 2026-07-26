package semantic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/semantic"
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
	if n := strings.Count(reqs[1].System, "Relevant memories:"); n != 1 {
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
