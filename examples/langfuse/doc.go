// Package main is a wire-contract smoke test for the Langfuse tracer adapter
// (components/tracers/langfuse). Unlike the unit tests — which point the client
// at an httptest server we control — this program sends a real agent run to a
// real Langfuse instance, so it validates that Langfuse actually accepts the
// batch envelope, event types, field names, timestamps, and Basic auth the
// adapter produces.
//
// It uses a scripted MockLLMClient, so no LLM provider key is needed; the only
// credentials required are Langfuse's, read from the environment:
//
//	LANGFUSE_PUBLIC_KEY   (required)
//	LANGFUSE_SECRET_KEY   (required)
//	LANGFUSE_HOST         (optional, defaults to https://cloud.langfuse.com)
//
// Run with:
//
//	LANGFUSE_PUBLIC_KEY=pk-... LANGFUSE_SECRET_KEY=sk-... go run ./examples/langfuse
//
// After it prints "flushed", open your Langfuse project and look for the most
// recent trace named "run" — it should contain a nested span per agent phase.
package main
