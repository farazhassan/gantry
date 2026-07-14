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
// ledger. Steps are matched by ID: for matches, only Status and Output are
// copied back (the run owns progress, the ledger owns structure). Projection
// steps the ledger has never seen — e.g. appended mid-run by the update_plan
// tool — are ADOPTED: appended to the ledger preserving their ids, with empty
// or colliding ids re-minted so the ledger's id key space stays unique. Ledger
// steps absent from the projection are left unchanged. Nil projection or nil
// ledger plan is a safe no-op.
func Flush(t *Task, proj *gantry.Plan) {
	if t == nil || t.Plan == nil || proj == nil {
		return
	}
	// Ids the ledger owned before this flush: projection steps matching these
	// reconcile in place; all other projection steps are adopted below.
	ledgerIDs := make(map[string]bool, len(t.Plan.Steps))
	for _, s := range t.Plan.Steps {
		if s.ID != "" {
			ledgerIDs[s.ID] = true
		}
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
	// Adopt projection-only steps. used is the collision domain for adopted
	// ids — the pre-flush ledger ids plus every id adopted so far — so
	// duplicate ids inside the projection are re-minted instead of dropped.
	used := make(map[string]bool, len(ledgerIDs))
	for id := range ledgerIDs {
		used[id] = true
	}
	for _, s := range proj.Steps {
		if s.ID != "" && ledgerIDs[s.ID] {
			continue // existing ledger step; progress reconciled above
		}
		ns := s
		if ns.Meta != nil {
			m := make(map[string]any, len(ns.Meta))
			for k, v := range ns.Meta {
				m[k] = v
			}
			ns.Meta = m
		}
		if ns.ID == "" || used[ns.ID] {
			ns.ID = mintStepID(used, len(t.Plan.Steps)+1)
		}
		used[ns.ID] = true
		t.Plan.Steps = append(t.Plan.Steps, ns)
	}
}
