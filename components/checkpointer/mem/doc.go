// Package mem implements checkpointer.Store as a process-local, in-memory map.
// Useful for tests and examples; state is lost on process exit and never
// shared across processes.
//
// Needs no third-party dependency, so unlike components/checkpointer/redis it
// lives in the root module. Wire it into an agent with components/checkpointer:
//
//	err := agent.With(checkpointer.New(mem.New(), "session-id"))
package mem
