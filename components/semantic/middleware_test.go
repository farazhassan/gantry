package semantic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/semantic"
	"github.com/farazhassan/gantry/eval"
)

// stubEmbedder returns canned vectors by exact text, defaulting to {1, 0}.
// It records each Embed call's texts so tests can assert batching.
type stubEmbedder struct {
	vecs  map[string][]float32
	calls [][]string
}

func (e *stubEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	e.calls = append(e.calls, append([]string(nil), texts...))
	out := make([][]float32, len(texts))
	for i, txt := range texts {
		if v, ok := e.vecs[txt]; ok {
			out[i] = v
		} else {
			out[i] = []float32{1, 0}
		}
	}
	return out, nil
}

func newAgent(t *testing.T, c gantry.Component, responses ...gantry.LLMResponse) (*gantry.Agent, *eval.MockLLMClient) {
	t.Helper()
	mock := eval.NewMockLLMClient(responses...)
	a, err := gantry.NewAgent(gantry.WithLLM(mock), gantry.WithMaxIterations(5))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(c); err != nil {
		t.Fatalf("install semantic: %v", err)
	}
	return a, mock
}

func TestRecallInjectsRelevantMemoriesIntoSystem(t *testing.T) {
	store := semantic.NewInMemoryStore()
	ctx := context.Background()
	_ = store.Add(ctx,
		semantic.Item{Text: "user likes puns", Vector: []float32{1, 0}},
		semantic.Item{Text: "unrelated fact", Vector: []float32{0, 1}},
	)
	emb := &stubEmbedder{}

	a, mock := newAgent(t, semantic.New(store, emb, semantic.WithK(1)),
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	if _, err := a.Run(ctx, "tell me a joke"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	if !strings.Contains(reqs[0].System, "Relevant memories:") {
		t.Errorf("System missing memories block: %q", reqs[0].System)
	}
	if !strings.Contains(reqs[0].System, "user likes puns") {
		t.Errorf("System missing recalled memory: %q", reqs[0].System)
	}
	if strings.Contains(reqs[0].System, "unrelated fact") {
		t.Errorf("System contains memory beyond k=1: %q", reqs[0].System)
	}
}

func TestRecallEmptyStoreLeavesSystemUntouched(t *testing.T) {
	a, mock := newAgent(t, semantic.New(semantic.NewInMemoryStore(), &stubEmbedder{}),
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sys := mock.Requests()[0].System; strings.Contains(sys, "Relevant memories:") {
		t.Errorf("System has memories block despite empty store: %q", sys)
	}
}

func TestRecallMinScoreFiltersWeakHits(t *testing.T) {
	store := semantic.NewInMemoryStore()
	// Orthogonal to the {1, 0} query vector: similarity 0 < 0.5 floor.
	_ = store.Add(context.Background(), semantic.Item{Text: "weak", Vector: []float32{0, 1}})
	a, mock := newAgent(t, semantic.New(store, &stubEmbedder{}, semantic.WithMinScore(0.5)),
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sys := mock.Requests()[0].System; strings.Contains(sys, "weak") {
		t.Errorf("System contains hit below min score: %q", sys)
	}
}
