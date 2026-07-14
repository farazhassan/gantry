// Package main demonstrates first-class transfer handoff: a router agent
// detects a "handoff" tool call via a PhasePostLLM routing middleware, which
// sets state.Handoff and terminates the run with DoneHandoff; a
// session.Manager configured with WithResolver then re-runs the same turn on
// the target agent from the accumulated transcript. Scripted MockLLMClients
// stand in for real providers, so the example is hermetic — it compiles and
// runs under `go test` with no API key.
//
// Run with:
//
//	go run ./examples/handoff
package main
