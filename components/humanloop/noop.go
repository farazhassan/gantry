package humanloop

import "context"

// NoOp always approves.
type NoOp struct{}

// NewNoOp returns a no-op HumanInLoop.
func NewNoOp() *NoOp { return &NoOp{} }

func (NoOp) Confirm(context.Context, Action) (Decision, error) {
	return Decision{Approved: true}, nil
}
