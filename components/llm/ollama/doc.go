// Package ollama provides a gantry.StreamingLLMClient backed by an
// Ollama server's /api/chat endpoint. It maps harness request/response types
// to Ollama's wire format, supports tool calling, and streams NDJSON deltas.
//
// Construct a client with New and point it at a server (defaults to
// http://localhost:11434):
//
//	client := ollama.New("llama3.1", ollama.WithBaseURL("http://host:11434"))
//	resp, err := client.Generate(ctx, gantry.LLMRequest{
//	    Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
//	})
//
// Ollama omits per-tool-call IDs, so the client synthesizes stable index-based
// IDs ("call-0", "call-1", ...) to satisfy the harness contract that links a
// tool result back to its call.
package ollama
