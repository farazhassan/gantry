package retriever

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// WithRetriever installs PhaseAssembleContext middleware that, on the first
// iteration, calls Retrieve(ctx, query, k), stores results in
// state.Retrieved, and appends a formatted block to state.System.
//
// PhaseAssembleContext re-runs every iteration and state.System persists, so
// retrieval and injection are guarded to iteration 0 to avoid stacking
// duplicate context blocks.
//
// The query used is state.Task if set, otherwise state.Input. Custom routing
// can be done by writing a higher-level component or using plain middleware.
func WithRetriever(a *gantry.Agent, r Retriever, k int) {
	const name = "components/retriever:retrieve"
	_ = a.UseNamed(gantry.PhaseAssembleContext, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, state *gantry.State) error {
			// Retrieve and inject only on the first iteration. state.System
			// persists across iterations, so appending every iteration would
			// stack duplicate context blocks.
			if state.Iteration == 0 {
				query := state.Task
				if query == "" {
					query = state.Input
				}
				docs, err := r.Retrieve(ctx, query, k)
				if err != nil {
					return err
				}
				state.Retrieved = docs

				if len(docs) > 0 {
					var b strings.Builder
					b.WriteString("\n\nRetrieved context:\n")
					for i, d := range docs {
						fmt.Fprintf(&b, "[%d] %s\n", i+1, d.Content)
					}
					state.System += b.String()
				}
			}
			return next(ctx, state)
		}
	})
}
