// Package main shows retrieval-augmented generation (RAG) in gantry. A static
// retriever is attached with retriever.New; on the first iteration it
// fetches the top-k documents, stores them in state.Retrieved, and appends them
// to the system prompt so the LLM can ground its answer. It uses a scripted
// MockLLMClient so it is hermetic.
//
// Run with:
//
//	go run ./examples/rag
package main
