// Package main is an end-to-end gantry example that wires every default
// phase and every first-class component together. It uses MockLLMClient
// (from eval) so the program is hermetic and CI-friendly.
//
// Run with:
//
//	go run ./examples/e2e
//
// The accompanying e2e_test.go runs the same flow as a test and asserts
// the agent terminates as expected.
package main
