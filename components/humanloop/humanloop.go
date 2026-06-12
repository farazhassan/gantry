// Package humanloop defines the HumanInLoop interface for pause-for-approval
// flows.
package humanloop

import "context"

// Action describes the action awaiting approval. Kind is "tool" for tool
// calls in PhaseToolExec; other kinds can be added by users.
type Action struct {
	Kind string
	Name string
	Args any
}

// Decision is the result of Confirm.
type Decision struct {
	Approved bool
	Reason   string
}

// HumanInLoop cooperatively pauses the loop to ask for approval.
type HumanInLoop interface {
	Confirm(ctx context.Context, action Action) (Decision, error)
}
