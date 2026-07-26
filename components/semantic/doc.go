// Package semantic provides vector-backed long-term memory for agents:
// past turns are embedded and stored in a Store; each new run recalls the
// top-k most similar memories into context automatically.
//
// This differs from components/memory (verbatim transcript persistence,
// replayed in full) and components/retriever (RAG over ingested documents):
// semantic memory accumulates from conversation and recalls by similarity.
// The recall middleware appends to state.System and does not touch
// state.Retrieved, which stays reserved for RAG documents.
//
// The Store interface deals in vectors, not text; the middleware composes a
// Store with an embeddings.Embeddings client. InMemoryStore is the reference
// implementation; components/sqlitevec provides a durable sqlite-vec backend
// in a separate Go module.
//
// Middleware ordering: the persist middleware does its work after next() on
// PhasePostLLM, so register semantic.New after critic.New and limiter.New so
// it observes the critic-finalized output. Order relative to memory.New does
// not matter: both persist middlewares only read the finalized state. See the
// New doc for what is and is not persisted.
package semantic
