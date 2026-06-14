// components/ask/auto.go
package ask

import "context"

// Auto is a Prompter that always returns a fixed Response. Useful for tests and
// headless runs (analogue of humanloop.AutoApprover).
type Auto struct {
	resp Response
}

// NewAuto returns an Auto that replies with resp to every Prompt call.
func NewAuto(resp Response) *Auto { return &Auto{resp: resp} }

// Prompt returns the canned response.
func (a *Auto) Prompt(context.Context, Request) (Response, error) {
	return a.resp, nil
}
