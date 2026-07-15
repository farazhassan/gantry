package task

import (
	"fmt"

	"github.com/farazhassan/gantry"
)

// DefaultOutputRuneBudget is the per-step Output rune cap Hydrate applies to
// completed (StepDone) steps when projecting the ledger into a run. 500 runes
// keeps a completed step's result visible as context without letting verbose
// step outputs snowball every subsequent run's prompt. The ledger itself
// always keeps the full Output; only the projection is bounded. Override per
// driver with WithHydrateOutputRunes, or call HydrateBounded directly.
const DefaultOutputRuneBudget = 500

// Hydrate projects a task's durable plan-ledger into the per-run *gantry.Plan
// the agent loop consumes, bounding completed steps' Output to
// DefaultOutputRuneBudget runes. See HydrateBounded for the full contract.
func Hydrate(t *Task) *gantry.Plan {
	return HydrateBounded(t, DefaultOutputRuneBudget)
}

// HydrateBounded is Hydrate with an explicit per-step Output rune budget. It
// returns an independent deep copy so the run can freely mutate step statuses
// without touching the ledger; Flush reconciles those changes back. Completed
// (StepDone) steps have Output truncated to outputRunes runes with a
// "… (truncated, N total)" suffix carrying the original rune count; steps in
// any other status are projected in full (the run may still be appending to
// them). outputRunes <= 0 disables bounding. Deterministic by design — no LLM
// summarization. Returns nil when the task has no plan.
func HydrateBounded(t *Task, outputRunes int) *gantry.Plan {
	if t == nil || t.Plan == nil {
		return nil
	}
	p := *t.Plan
	// cloneSteps deep-copies the Steps slice and each step's Meta map so the run
	// can mutate the projection without touching the ledger (see inmem.go).
	p.Steps = cloneSteps(t.Plan.Steps)
	if outputRunes > 0 {
		for i := range p.Steps {
			if p.Steps[i].Status == gantry.StepDone {
				p.Steps[i].Output = truncateRunes(p.Steps[i].Output, outputRunes)
			}
		}
	}
	return &p
}

// truncateRunes bounds s to max runes, marking the cut with a suffix carrying
// the original rune count. Strings within the budget are returned unchanged.
func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + fmt.Sprintf("… (truncated, %d total)", len(runes))
}

// Flush reconciles run-made changes in the projection back into the task's
// ledger. Steps are matched by ID: for matches, only Status and Output are
// copied back (the run owns progress, the ledger owns structure) — with one
// guard: a step that was StepDone in the ledger AND is still StepDone in the
// projection is left untouched, because the projection's Output may be the
// truncated form from HydrateBounded and a done step's Output is settled
// history. A run that re-opens a step (projection status != done) regains
// write access to it. Projection steps the ledger has never seen — e.g.
// appended mid-run by the update_plan tool — are ADOPTED: appended to the
// ledger preserving their ids, with empty or colliding ids re-minted so the
// ledger's id key space stays unique. Ledger steps absent from the projection
// are left unchanged. Nil projection or nil ledger plan is a safe no-op.
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
		upd, ok := byID[t.Plan.Steps[i].ID]
		if !ok {
			continue
		}
		if t.Plan.Steps[i].Status == gantry.StepDone && upd.Status == gantry.StepDone {
			continue // settled: never overwrite a done step's full Output
		}
		t.Plan.Steps[i].Status = upd.Status
		t.Plan.Steps[i].Output = upd.Output
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
