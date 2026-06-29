// Package main shows how to persist and restore run state with a checkpointer.
// An in-memory checkpointer is attached with checkpointer.New; it
// saves the final state during PhaseEnd. After the run, the example loads the
// saved state back by id and compares it to the live run — the save/load
// round-trip from e2e, isolated here. It uses a scripted MockLLMClient so it
// is hermetic.
//
// Run with:
//
//	go run ./examples/checkpoint
package main
