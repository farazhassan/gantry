package task

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// adoptOrFlush moves a run's projected plan back into the durable ledger. On a
// task's first run the ledger is empty, so the plan is born here: adopt copies
// it in and assigns stable step IDs. On every later run the ledger already owns
// the structure, so the Phase-1 Flush reconciles progress (Status/Output) by ID.
func adoptOrFlush(t *Task, projected *gantry.Plan) {
	if t.Plan == nil || len(t.Plan.Steps) == 0 {
		adopt(t, projected)
		return
	}
	Flush(t, projected)
}

// adopt installs projected as the task's ledger plan, deep-copying it (so the
// run's State and the ledger never share backing storage) and assigning
// deterministic s1, s2, … IDs to any step that lacks one. A nil or empty
// projection is a no-op: a planless first run leaves the task without a plan.
func adopt(t *Task, projected *gantry.Plan) {
	if projected == nil || len(projected.Steps) == 0 {
		return
	}
	clone := *projected
	clone.Steps = cloneSteps(projected.Steps)
	for i := range clone.Steps {
		if clone.Steps[i].ID == "" {
			clone.Steps[i].ID = fmt.Sprintf("s%d", i+1)
		}
	}
	t.Plan = &clone
}
