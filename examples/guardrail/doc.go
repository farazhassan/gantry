// Package main contrasts gantry's two termination categories with two short
// scenarios. A guardrail block is an active stop: Run sets DoneGuardrailBlocked
// and returns the ErrGuardrailBlocked sentinel (check with errors.Is). A budget
// stop is a resource stop: Run sets DoneBudgetExceeded and returns a nil error.
// Knowing which stops return an error and which don't is the subtlest part of
// the Run contract. Both scenarios use a scripted MockLLMClient, so the
// example is hermetic.
//
// Run with:
//
//	go run ./examples/guardrail
package main
