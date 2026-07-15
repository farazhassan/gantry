package planner

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/task"
)

// LLMReplanner revises a task's plan mid-flight via the same forced
// propose_plan call LLMPlanner uses, with the current ledger (step ids and
// statuses, via renderPlan) and the trigger's reason rendered into the
// prompt. It implements task.Replanner for task.WithReplanner.
//
// Unlike LLMPlanner.Plan there is deliberately NO legacy text fallback here:
// the task.Driver already degrades gracefully on a Replanner error (the
// rejection critique hint stands alone and the task continues), so an error
// is simply returned.
type LLMReplanner struct {
	client gantry.LLMClient
	rubric string
}

// NewLLMReplanner returns an LLM-driven Replanner. Wire it into a Driver with
// task.WithReplanner(planner.NewLLMReplanner(client, rubric)).
func NewLLMReplanner(client gantry.LLMClient, rubric string) *LLMReplanner {
	return &LLMReplanner{client: client, rubric: rubric}
}

// compile-time check: LLMReplanner implements task.Replanner.
var _ task.Replanner = (*LLMReplanner)(nil)

// Replan prompts for ONLY the new steps to append; the Driver preserves the
// existing ledger and merges via the ledger's adoptOrFlush (plan 12's relaxed
// Flush appends the new steps). Returned steps carry empty
// IDs — the ledger mints them at adoption, exactly as with initial planning.
func (r *LLMReplanner) Replan(ctx context.Context, t *task.Task, reason string) (*gantry.Plan, error) {
	var b strings.Builder
	b.WriteString("The plan for this task needs revision.\n\nGoal: ")
	b.WriteString(t.Goal)
	if ledger := renderPlan(t.Plan); ledger != "" {
		b.WriteString("\n\nCurrent plan (step ids and statuses):")
		b.WriteString(ledger)
	}
	b.WriteString("\nRevision reason: ")
	b.WriteString(reason)
	b.WriteString("\n\nPropose ONLY the new steps to append; existing steps and their statuses are kept as-is.")

	req := gantry.LLMRequest{
		System:     r.rubric,
		Messages:   []gantry.Message{{Role: gantry.RoleUser, Content: b.String()}},
		Tools:      []gantry.ToolDef{proposePlanDef()},
		ToolChoice: &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: proposePlanName},
	}
	resp, err := r.client.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	steps, err := parseProposedSteps(resp)
	if err != nil {
		return nil, err
	}
	return &gantry.Plan{Goal: t.Goal, Steps: steps}, nil
}
