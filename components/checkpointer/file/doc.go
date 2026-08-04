// Package file implements checkpointer.Store as one file per id under a
// directory, with atomic (write-temp-then-rename) writes. Suitable for
// single-host resume across process restarts.
//
// Needs no third-party dependency, so unlike components/checkpointer/redis it
// lives in the root module. Wire it into an agent with components/checkpointer:
//
//	cp, err := file.New("./checkpoints")
//	// handle err
//	err = agent.With(checkpointer.New(cp, "session-id"))
package file
