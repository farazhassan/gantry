package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/farazhassan/gantry"
)

// UpdatePlanToolName is the pseudo-tool name the UpdatePlan component
// intercepts. It is never registered with a tool Registry: calls are applied
// directly to state.Plan by planner middleware, because tool.Tool.Invoke
// receives only ctx and input and cannot reach the run's *State.
const UpdatePlanToolName = "update_plan"

// UpdatePlanToolDef describes the update_plan pseudo-tool to the model. The
// step_id values it takes are the (id) tags renderPlan shows in the injected
// Plan block.
func UpdatePlanToolDef() gantry.ToolDef {
	return gantry.ToolDef{
		Name: UpdatePlanToolName,
		Description: "Update the plan: set a step's status, record a step's output, or " +
			"append a new step. Reference steps by the (id) shown in the Plan block. " +
			"Ops apply in order; each op succeeds or fails independently and the " +
			"result reports every outcome (add_step results include the new step's id).",
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "ops": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "op": {"type": "string", "enum": ["set_status", "set_output", "add_step"]},
          "step_id": {"type": "string", "description": "Target step id; required for set_status and set_output."},
          "status": {"type": "string", "enum": ["pending", "active", "done", "failed", "skipped"], "description": "New status; required for set_status."},
          "output": {"type": "string", "description": "Short result/artifact summary; used by set_output."},
          "description": {"type": "string", "description": "New step description; required for add_step."},
          "acceptance_criteria": {"type": "array", "items": {"type": "string"}, "description": "Done-contract for an added step."}
        },
        "required": ["op"]
      }
    }
  },
  "required": ["ops"]
}`),
	}
}

// planOp is one decoded update_plan operation.
type planOp struct {
	Op                 string   `json:"op"`
	StepID             string   `json:"step_id"`
	Status             string   `json:"status"`
	Output             string   `json:"output"`
	Description        string   `json:"description"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

// updatePlanInput is the decoded tool-call envelope.
type updatePlanInput struct {
	Ops []planOp `json:"ops"`
}

// opResult reports one op's outcome inside the synthesized tool result. StepID
// carries the target id on success — for add_step that is the freshly minted
// id, so the model can reference the new step immediately.
type opResult struct {
	OK     bool   `json:"ok"`
	StepID string `json:"step_id,omitempty"`
	Error  string `json:"error,omitempty"`
}

// runUpdatePlan decodes one update_plan call and applies its ops to plan,
// returning the synthesized tool-result content. isErr reports an
// envelope-level failure (no plan, malformed JSON, empty ops); per-op failures
// are reported in-band in the results payload and are NOT envelope errors —
// the model reads them and corrects itself on the next turn.
func runUpdatePlan(plan *gantry.Plan, input json.RawMessage) (content string, isErr bool) {
	if plan == nil {
		return "update_plan: no plan exists on this run", true
	}
	var in updatePlanInput
	if err := json.Unmarshal(input, &in); err != nil {
		return "update_plan: invalid input: " + err.Error(), true
	}
	if len(in.Ops) == 0 {
		return "update_plan: ops must be a non-empty array", true
	}
	out, err := json.Marshal(struct {
		Results []opResult `json:"results"`
	}{Results: applyOps(plan, in.Ops)})
	if err != nil {
		return "update_plan: encode results: " + err.Error(), true
	}
	return string(out), false
}

// applyOps mutates plan in place, one op at a time. A failed op (unknown
// step_id, invalid status, missing fields) does not stop later ops.
func applyOps(plan *gantry.Plan, ops []planOp) []opResult {
	results := make([]opResult, len(ops))
	for i, op := range ops {
		results[i] = applyOp(plan, op)
	}
	return results
}

func applyOp(plan *gantry.Plan, op planOp) opResult {
	switch op.Op {
	case "set_status":
		st, ok := parseStatus(op.Status)
		if !ok {
			return opResult{Error: fmt.Sprintf("invalid status %q", op.Status)}
		}
		step := findStep(plan, op.StepID)
		if step == nil {
			return opResult{Error: fmt.Sprintf("unknown step_id %q", op.StepID)}
		}
		step.Status = st
		return opResult{OK: true, StepID: op.StepID}
	case "set_output":
		step := findStep(plan, op.StepID)
		if step == nil {
			return opResult{Error: fmt.Sprintf("unknown step_id %q", op.StepID)}
		}
		step.Output = op.Output
		return opResult{OK: true, StepID: op.StepID}
	case "add_step":
		if strings.TrimSpace(op.Description) == "" {
			return opResult{Error: "add_step requires a non-empty description"}
		}
		id := mintStepID(plan)
		plan.Steps = append(plan.Steps, gantry.PlanStep{
			ID:                 id,
			Description:        op.Description,
			Status:             gantry.StepPending,
			AcceptanceCriteria: strings.Join(op.AcceptanceCriteria, "; "),
		})
		return opResult{OK: true, StepID: id}
	default:
		return opResult{Error: fmt.Sprintf("unknown op %q", op.Op)}
	}
}

// parseStatus validates a wire status against the gantry.StepStatus set.
func parseStatus(s string) (gantry.StepStatus, bool) {
	switch st := gantry.StepStatus(s); st {
	case gantry.StepPending, gantry.StepActive, gantry.StepDone, gantry.StepFailed, gantry.StepSkipped:
		return st, true
	default:
		return "", false
	}
}

// findStep returns a pointer into plan.Steps for the step with the given id,
// or nil when the id is empty or unknown.
func findStep(plan *gantry.Plan, id string) *gantry.PlanStep {
	if id == "" {
		return nil
	}
	for i := range plan.Steps {
		if plan.Steps[i].ID == id {
			return &plan.Steps[i]
		}
	}
	return nil
}

// mintStepID returns a fresh "s<n>" id that collides with no existing step id.
// It mirrors the task ledger's adoption convention (task/adopt.go names steps
// s1..sN by position): the candidate starts at len(Steps)+1 and skips forward
// over ids the plan already uses.
func mintStepID(plan *gantry.Plan) string {
	used := make(map[string]bool, len(plan.Steps))
	for _, s := range plan.Steps {
		used[s.ID] = true
	}
	for n := len(plan.Steps) + 1; ; n++ {
		id := fmt.Sprintf("s%d", n)
		if !used[id] {
			return id
		}
	}
}
