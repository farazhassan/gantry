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

	// Captured on PhasePostLLM rather than PhaseAssembleContext: empirically,
	// registering "test:capture" on PhaseAssembleContext makes it the
	// outermost middleware in that phase's chain (Compose wraps
	// last-registered outermost), so its pre-next check runs before the
	// semantic component's recall middleware sets the stash. PhasePostLLM
	// runs after PhaseAssembleContext completes for the same iteration, on
	// the same *gantry.State, so Meta is already populated by then.
	var recalled []semantic.Hit
	if err := a.UseNamed(gantry.PhasePostLLM, "test:capture", func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			if hits, ok := s.Meta[semantic.MetaRecalled].([]semantic.Hit); ok {
				recalled = hits
			}
			return next(ctx, s)
		}
	}); err != nil {
		t.Fatalf("install capture middleware: %v", err)
	}

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
	if len(recalled) != 1 {
		t.Fatalf("recalled = %d hits, want 1", len(recalled))
	}
	if recalled[0].Text != "user likes puns" {
		t.Errorf("recalled[0].Text = %q, want %q", recalled[0].Text, "user likes puns")
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
	// Aligned with the {1, 0} query vector: similarity 1.0 >= 0.5 floor.
	_ = store.Add(context.Background(), semantic.Item{Text: "strong signal", Vector: []float32{1, 0}})
	a, mock := newAgent(t, semantic.New(store, &stubEmbedder{}, semantic.WithMinScore(0.5)),
		gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sys := mock.Requests()[0].System
	if strings.Contains(sys, "weak") {
		t.Errorf("System contains hit below min score: %q", sys)
	}
	if !strings.Contains(sys, "strong signal") {
		t.Errorf("System missing hit above min score: %q", sys)
	}
}
