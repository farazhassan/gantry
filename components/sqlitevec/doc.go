// Package sqlitevec implements vectorstore.Store on SQLite with the sqlite-vec
// extension: a local, serverless vector store in a single database file.
//
// The stack is pure Go — github.com/ncruces/go-sqlite3 runs SQLite compiled
// to WASM (via wazero), and the sqlite-vec bindings supply a WASM build with
// vec0 compiled in — so there is no CGO and no C toolchain requirement.
//
// This package lives in its own Go module so the root gantry module stays
// dependency-free. Wire it into an agent with components/memory:
//
//	store, err := sqlitevec.New("agent.db", sqlitevec.WithDim(1536))
//	// handle err
//	defer store.Close()
//	err = agent.With(memory.New(store, embeddingsClient))
//
// Item metadata is stored as JSON, so metadata values are subject to JSON
// type normalization on read — notably, numbers come back as float64
// regardless of the Go type written. Keep metadata values to strings and
// booleans, or expect float64 for any numeric value.
package sqlitevec
