package task

import "github.com/farazhassan/gantry"

// Hydrate projects a task's durable plan-ledger into the per-run *gantry.Plan
// the agent loop consumes. It returns an independent deep copy so the run can
// freely mutate step statuses without touching the ledger; Flush reconciles
// those changes back. Returns nil when the task has no plan.
//
// Plan 1 copies the ledger faithfully. A later plan may summarize completed
// steps' Output to bound tokens (spec §7); that optimization changes only what
// this function emits, not its contract.
func Hydrate(t *Task) *gantry.Plan {
	if t == nil || t.Plan == nil {
		return nil
	}
	p := *t.Plan
	// cloneSteps deep-copies the Steps slice and each step's Meta map so the run
	// can mutate the projection without touching the ledger (see inmem.go).
	p.Steps = cloneSteps(t.Plan.Steps)
	return &p
}

// Flush reconciles run-made changes in the projection back into the task's
// ledger, matching steps by ID. Only Status and Output are copied back (the run
// owns progress, the ledger owns structure). Steps present in the projection
// but not the ledger are ignored; ledger steps absent from the projection are
// left unchanged. Steps with empty IDs in either the ledger or projection are
// not reconciled (there is no key to match on). Nil projection or nil ledger
// plan is a safe no-op.
func Flush(t *Task, proj *gantry.Plan) {
	if t == nil || t.Plan == nil || proj == nil {
		return
	}
	byID := make(map[string]gantry.PlanStep, len(proj.Steps))
	for _, s := range proj.Steps {
		if s.ID != "" {
			byID[s.ID] = s
		}
	}
	for i := range t.Plan.Steps {
		if upd, ok := byID[t.Plan.Steps[i].ID]; ok {
			t.Plan.Steps[i].Status = upd.Status
			t.Plan.Steps[i].Output = upd.Output
		}
	}
}
