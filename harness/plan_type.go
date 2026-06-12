package harness

// Plan is the output of a Planner. PlanStep is intentionally permissive:
// the only required field is Description. Concrete planners can stash
// arbitrary data on Meta.
type Plan struct {
	Goal  string
	Steps []PlanStep
}

// PlanStep is one item in a Plan. Description is human-readable;
// Meta carries arbitrary planner-specific data.
type PlanStep struct {
	Description string
	Meta        map[string]any
}
