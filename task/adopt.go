package task

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// adoptOrFlush moves a run's projected plan back into the durable ledger. On a
// task's first run the ledger is empty, so the plan is born here: adopt copies
// it in and assigns stable step IDs. On every later run the ledger already owns
// the structure, so Flush reconciles progress (Status/Output) by ID and adopts
// any steps the run appended (e.g. via the update_plan tool).
func adoptOrFlush(t *Task, projected *gantry.Plan) {
	if t.Plan == nil || len(t.Plan.Steps) == 0 {
		adopt(t, projected)
		return
	}
	Flush(t, projected)
}

// adopt installs projected as the task's ledger plan, deep-copying it (so the
// run's State and the ledger never share backing storage) and assigning
// deterministic s1, s2, … IDs to any step that lacks one. Planner-provided ids
// are preserved but collision-checked: a duplicate id later in the slice is
// re-minted, and minted ids skip over every id already claimed — duplicate
// ledger keys would silently corrupt every future Flush reconciliation. A nil
// or empty projection is a no-op: a planless first run leaves the task without
// a plan.
func adopt(t *Task, projected *gantry.Plan) {
	if projected == nil || len(projected.Steps) == 0 {
		return
	}
	clone := *projected
	clone.Steps = cloneSteps(projected.Steps)
	// Pass 1: claim planner-provided ids; blank same-slice duplicates so pass 2
	// re-mints them.
	used := make(map[string]bool, len(clone.Steps))
	for i := range clone.Steps {
		id := clone.Steps[i].ID
		if id == "" {
			continue
		}
		if used[id] {
			clone.Steps[i].ID = ""
			continue
		}
		used[id] = true
	}
	// Pass 2: mint ids for id-less steps, skipping over claimed ids.
	for i := range clone.Steps {
		if clone.Steps[i].ID != "" {
			continue
		}
		id := mintStepID(used, i+1)
		clone.Steps[i].ID = id
		used[id] = true
	}
	t.Plan = &clone
}

// mintStepID returns the first "s<k>" id with k >= n that is not claimed in
// used. It does not record the returned id in used; callers do. Shared by
// adopt (first-run id assignment) and Flush (mid-run step adoption).
func mintStepID(used map[string]bool, n int) string {
	for {
		id := fmt.Sprintf("s%d", n)
		if !used[id] {
			return id
		}
		n++
	}
}
