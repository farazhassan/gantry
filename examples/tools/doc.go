// Package main gives a gantry agent a single tool and watches it use it. It
// shows how to define a Tool (Definition + Invoke), register it with
// tool.FromTools, and how the loop folds PhaseToolExec -> PhaseObserve: the
// model calls the tool, the result is fed back, and a second turn produces the
// final answer. It uses a scripted MockLLMClient so it is hermetic.
//
// Run with:
//
//	go run ./examples/tools
package main
