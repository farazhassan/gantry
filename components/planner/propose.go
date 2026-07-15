package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// proposePlanName is the tool the planner forces the model to call
// (gantry.ToolChoiceTool) so plan output arrives as schema-validated JSON
// instead of free text. Shared by LLMPlanner.Plan and LLMReplanner.Replan.
const proposePlanName = "propose_plan"

// proposePlanDef describes the propose_plan tool. acceptance_criteria is an
// array of strings on the wire; parseProposedSteps joins the entries with
// "; " into PlanStep.AcceptanceCriteria (a single string).
func proposePlanDef() gantry.ToolDef {
	return gantry.ToolDef{
		Name: proposePlanName,
		Description: "Propose the plan as an ordered list of steps. Each step has a " +
			"description and optional acceptance criteria (checks that must hold " +
			"for the step to count as done).",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "steps": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "description": {"type": "string", "description": "What this step does."},
          "acceptance_criteria": {
            "type": "array",
            "items": {"type": "string"},
            "description": "Checks that must hold for this step to count as done."
          }
        },
        "required": ["description"]
      }
    }
  },
  "required": ["steps"]
}`),
	}
}

// parseProposedSteps extracts plan steps from a forced propose_plan response.
// Step IDs are left empty — the task ledger mints them at adoption time
// (task's adopt / relaxed Flush, via adoptOrFlush), exactly as with the
// legacy text parser.
// Steps with a blank description are dropped. A response without a
// propose_plan call, or with an undecodable payload, is an error: when the
// provider honored the forced ToolChoice the call is guaranteed, so its
// absence means the client is misbehaving and silent tolerance would hide it.
func parseProposedSteps(resp gantry.LLMResponse) ([]gantry.PlanStep, error) {
	var call *gantry.ToolCall
	for i := range resp.ToolCalls {
		if resp.ToolCalls[i].Name == proposePlanName {
			call = &resp.ToolCalls[i]
			break
		}
	}
	if call == nil {
		return nil, fmt.Errorf("planner: response carries no %s tool call", proposePlanName)
	}
	var payload struct {
		Steps []struct {
			Description        string   `json:"description"`
			AcceptanceCriteria []string `json:"acceptance_criteria"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(call.Input, &payload); err != nil {
		return nil, fmt.Errorf("planner: decode %s payload: %w", proposePlanName, err)
	}
	var steps []gantry.PlanStep
	for _, s := range payload.Steps {
		desc := strings.TrimSpace(s.Description)
		if desc == "" {
			continue
		}
		steps = append(steps, gantry.PlanStep{
			Description:        desc,
			AcceptanceCriteria: strings.Join(s.AcceptanceCriteria, "; "),
		})
	}
	return steps, nil
}
