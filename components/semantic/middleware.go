package semantic

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/embeddings"
)

// MetaRecalled is the state.Meta key under which the recall middleware
// stashes the []Hit recalled this run (for observability and tests).
const MetaRecalled = "components/semantic:recalled"

const defaultK = 4

type component struct {
	store       Store
	emb         embeddings.Embeddings
	k           int
	minScore    float64
	minScoreSet bool
}

// Option configures the component returned by New.
type Option func(*component)

// WithK sets how many memories are recalled per run (default 4).
// Non-positive values are ignored.
func WithK(k int) Option {
	return func(c *component) {
		if k > 0 {
			c.k = k
		}
	}
}

// WithMinScore drops recalled hits whose Score is below s. By default no
// floor is applied (even negative-similarity hits pass through, subject to k).
// Passing WithMinScore(0) applies a floor at 0 (dropping negative-similarity
// hits), which differs from leaving it unset.
func WithMinScore(s float64) Option {
	return func(c *component) {
		c.minScore = s
		c.minScoreSet = true
	}
}

// New returns a Component that wires semantic memory into the agent. It
// installs a PhaseAssembleContext "components/semantic:recall" middleware
// that, on iteration 0, embeds the query (state.Task if set, else
// state.Input), searches the store, and appends a "Relevant memories:" block
// to state.System.
func New(store Store, emb embeddings.Embeddings, opts ...Option) gantry.Component {
	c := &component{store: store, emb: emb, k: defaultK}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *component) Install(a *gantry.Agent) error {
	const recallName = "components/semantic:recall"

	return a.UseNamed(gantry.PhaseAssembleContext, recallName, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, s *gantry.State) error {
			// Recall only on the first iteration: state.System persists
			// across iterations, so appending every iteration would stack
			// duplicate memory blocks.
			if s.Iteration == 0 {
				if err := c.recall(ctx, s); err != nil {
					return err
				}
			}
			return next(ctx, s)
		}
	})
}

func (c *component) recall(ctx context.Context, s *gantry.State) error {
	query := s.Task
	if query == "" {
		query = s.Input
	}
	if query == "" {
		return nil
	}
	vecs, err := c.emb.Embed(ctx, []string{query})
	if err != nil {
		return fmt.Errorf("semantic: embed query: %w", err)
	}
	if len(vecs) != 1 {
		return fmt.Errorf("semantic: embedder returned %d vectors for 1 query", len(vecs))
	}
	hits, err := c.store.Search(ctx, vecs[0], c.k)
	if err != nil {
		return fmt.Errorf("semantic: search: %w", err)
	}
	if c.minScoreSet {
		kept := hits[:0:0]
		for _, h := range hits {
			if h.Score >= c.minScore {
				kept = append(kept, h)
			}
		}
		hits = kept
	}
	if s.Meta == nil {
		s.Meta = map[string]any{}
	}
	s.Meta[MetaRecalled] = hits
	if len(hits) > 0 {
		var b strings.Builder
		b.WriteString("\n\nRelevant memories:\n")
		for i, h := range hits {
			fmt.Fprintf(&b, "[%d] %s\n", i+1, h.Text)
		}
		s.System += b.String()
	}
	return nil
}
