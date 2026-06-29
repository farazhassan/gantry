package gantry

// Plan is the output of a Planner. PlanStep is intentionally permissive:
// the only required field is Description. Concrete planners can stash
// arbitrary data on Meta.
type Plan struct {
	Goal  string
	Steps []PlanStep
}

// StepStatus is the lifecycle state of a single PlanStep. The zero value is the
// empty string, which callers that predate the Task layer leave unset; treat an
// empty status as "not tracked" (rendered without a status tag).
type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepActive  StepStatus = "active"
	StepDone    StepStatus = "done"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

// PlanStep is one item in a Plan. Description is human-readable; Meta carries
// arbitrary planner-specific data.
//
// The remaining fields support the Task ledger (see the task package): ID
// gives each step a stable identity so a per-run projection can be reconciled
// back into the durable ledger; Status tracks progress; AcceptanceCriteria is
// the "done when…" set at decomposition time; Output is a short artifact/result
// summary once the step is done. All are optional and zero-valued for planners
// that do not use the ledger.
type PlanStep struct {
	ID                 string
	Description        string
	Status             StepStatus
	AcceptanceCriteria string
	Output             string
	Meta               map[string]any
}
