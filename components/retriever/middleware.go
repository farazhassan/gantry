package retriever

import (
	"context"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

type component struct {
	r Retriever
	k int
}

// New returns a Component that installs PhaseAssembleContext middleware which, on
// iteration 0, calls Retrieve(ctx, query, k), stores results in state.Retrieved,
// and appends a formatted block to state.System. The query is state.Task if set,
// otherwise state.Input.
//
// PhaseAssembleContext re-runs every iteration and state.System persists, so
// retrieval and injection are guarded to iteration 0 to avoid stacking
// duplicate context blocks.
//
// Custom routing can be done by writing a higher-level component or using plain middleware.
func New(r Retriever, k int) gantry.Component { return &component{r: r, k: k} }

func (c *component) Install(a *gantry.Agent) error {
	const name = "components/retriever:retrieve"
	return a.UseNamed(gantry.PhaseAssembleContext, name, func(next gantry.Handler) gantry.Handler {
		return func(ctx context.Context, state *gantry.State) error {
			// Retrieve and inject only on the first iteration. state.System
			// persists across iterations, so appending every iteration would
			// stack duplicate context blocks.
			if state.Iteration == 0 {
				query := state.Task
				if query == "" {
					query = state.Input
				}
				docs, err := c.r.Retrieve(ctx, query, c.k)
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
