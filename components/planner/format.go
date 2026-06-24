package planner

import (
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// renderPlan formats a plan as an injectable "Plan:" block. Steps with a
// non-empty Status get a "[status]" tag; steps without one render as plain
// "N. description" for backward compatibility with planners that predate the
// task ledger. An empty or nil plan renders to the empty string.
func renderPlan(p *gantry.Plan) string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nPlan:\n")
	for i, step := range p.Steps {
		if step.Status != "" {
			fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, step.Status, step.Description)
		} else {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step.Description)
		}
	}
	return b.String()
}
