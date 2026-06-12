// Package main is the smallest possible gantry agent: an LLMClient and nothing
// else. It shows the core loop (assemble -> call -> observe), how a response
// with no tool calls terminates the run with DoneNoToolCalls, and where the
// answer lands (state.FinalOutput). It uses a scripted MockLLMClient so it is
// hermetic.
//
// Run with:
//
//	go run ./examples/minimal
package main
