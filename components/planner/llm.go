package planner

import (
	"context"
	"strings"

	"github.com/farazhassan/gantry"
)

// LLMPlanner generates a Plan by prompting an LLM with a rubric.
// It splits the response into newline-separated steps and trims any
// leading list markers ("1.", "1)", "- ", "* ").
type LLMPlanner struct {
	client gantry.LLMClient
	rubric string
}

// NewLLM returns an LLM-driven Planner.
func NewLLM(client gantry.LLMClient, rubric string) *LLMPlanner {
	return &LLMPlanner{client: client, rubric: rubric}
}

func (p *LLMPlanner) Plan(ctx context.Context, task string) (*gantry.Plan, error) {
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
