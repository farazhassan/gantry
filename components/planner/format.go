package planner

import (
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// renderPlan formats a plan as an injectable "Plan:" block. The full ledger
// form is "N. [status] (id) description — criteria: ..."; each ledger field
// (status, id, acceptance criteria) renders only when present, so steps from
// planners that predate the task ledger fall back to plain "N. description".
// The (id) is what the model passes as step_id in update_plan calls, and the
// criteria line is the step's done-contract. An empty or nil plan renders to
// the empty string.
func renderPlan(p *gantry.Plan) string {
	if p == nil || len(p.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nPlan:\n")
	for i, step := range p.Steps {
		fmt.Fprintf(&b, "%d.", i+1)
		if step.Status != "" {
			fmt.Fprintf(&b, " [%s]", step.Status)
		}
		if step.ID != "" {
			fmt.Fprintf(&b, " (%s)", step.ID)
		}
		fmt.Fprintf(&b, " %s", step.Description)
		if step.AcceptanceCriteria != "" {
			fmt.Fprintf(&b, " — criteria: %s", step.AcceptanceCriteria)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
