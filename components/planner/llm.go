package planner

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry"
)

// LLMPlanner generates a Plan by forcing the model to call the propose_plan
// tool (gantry.ToolChoiceTool), so steps and acceptance criteria arrive as
// schema-validated JSON. When the configured client cannot honor the forced
// ToolChoice — Generate returns an error, e.g. the Ollama adapter rejects
// forced modes client-side — Plan falls back to ONE legacy plain-text request
// parsed line-by-line (" :: " splits acceptance criteria from a description).
type LLMPlanner struct {
	client gantry.LLMClient
	rubric string
}

// NewLLM returns an LLM-driven Planner.
func NewLLM(client gantry.LLMClient, rubric string) *LLMPlanner {
	return &LLMPlanner{client: client, rubric: rubric}
}

// Plan asks the model for a structured plan via a forced propose_plan call.
// Step IDs are left empty — the task ledger mints them at adoption, as today.
//
// Fallback decision (explicit): adapters return plain errors for unsupported
// ToolChoice modes (no sentinel exists), so "unsupported" cannot be told apart
// from a transient failure. Therefore ANY Generate error on the forced request
// triggers exactly one legacy retry without tools. A transient failure fails
// the legacy attempt too and that error is returned; the extra round-trip
// happens only on error paths, never on the happy path. A SUCCESSFUL response
// missing the forced call does NOT fall back — that is a misbehaving client
// and is surfaced as an error.
func (p *LLMPlanner) Plan(ctx context.Context, task string) (*gantry.Plan, error) {
	req := gantry.LLMRequest{
		System:     p.rubric,
		Messages:   []gantry.Message{{Role: gantry.RoleUser, Content: task}},
		Tools:      []gantry.ToolDef{proposePlanDef()},
		ToolChoice: &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: proposePlanName},
	}
	resp, err := p.client.Generate(ctx, req)
	if err != nil {
		return p.planLegacy(ctx, task)
	}
	steps, err := parseProposedSteps(resp)
	if err != nil {
		return nil, err
	}
	return &gantry.Plan{Goal: task, Steps: steps}, nil
}

// planLegacy is the pre-ToolChoice path: prompt without tools, split the text
// reply into newline-separated steps, trim list markers, and cut each line on
// " :: " into description and acceptance criteria. Kept ONLY as the fallback
// for clients that error on a forced ToolChoice.
func (p *LLMPlanner) planLegacy(ctx context.Context, task string) (*gantry.Plan, error) {
	req := gantry.LLMRequest{
		System: p.rubric,
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: task},
		},
	}
	resp, err := p.client.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	plan := &gantry.Plan{Goal: task}
	for _, line := range strings.Split(resp.Content, "\n") {
		line = strings.TrimSpace(stripListMarker(line))
		if line == "" {
			continue
		}
		desc, criteria := splitCriteria(line)
		plan.Steps = append(plan.Steps, gantry.PlanStep{Description: desc, AcceptanceCriteria: criteria})
	}
	return plan, nil
}

func stripListMarker(line string) string {
	line = strings.TrimSpace(line)
	// Strip "1.", "12)", "- ", "* "
	if len(line) >= 2 && (line[0] == '-' || line[0] == '*') && line[1] == ' ' {
		return line[2:]
	}
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i > 0 && i < len(line) && (line[i] == '.' || line[i] == ')') {
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

// splitCriteria splits a plan line on the first " :: " into a description and
// its acceptance criteria. A line without the delimiter yields the whole line
// as the description and empty criteria (backward compatible).
func splitCriteria(line string) (desc, criteria string) {
	if d, c, ok := strings.Cut(line, " :: "); ok {
		return strings.TrimSpace(d), strings.TrimSpace(c)
	}
	return line, ""
}
