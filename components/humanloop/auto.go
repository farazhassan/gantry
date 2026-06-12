package humanloop

import "context"

// AutoApprover always approves. Identical to NoOp; kept as a distinct
// name for clarity in tests.
type AutoApprover struct{}

// NewAutoApprover returns an always-approve impl.
func NewAutoApprover() *AutoApprover { return &AutoApprover{} }

func (AutoApprover) Confirm(context.Context, Action) (Decision, error) {
	return Decision{Approved: true}, nil
}

// AutoDenier always denies with the configured reason.
type AutoDenier struct {
	reason string
}

// NewAutoDenier returns an always-deny impl.
func NewAutoDenier(reason string) *AutoDenier { return &AutoDenier{reason: reason} }

func (d *AutoDenier) Confirm(context.Context, Action) (Decision, error) {
	return Decision{Approved: false, Reason: d.reason}, nil
}
