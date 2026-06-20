// Package openrouter provides a gantry.StreamingLLMClient backed by
// OpenRouter's OpenAI-compatible /v1/chat/completions endpoint. It maps gantry
// request/response types to that wire format, supports tool calling, and
// streams Server-Sent Events.
//
// Construct a client with New. The API key comes from WithAPIKey or, failing
// that, the OPENROUTER_API_KEY environment variable. Model ids are namespaced
// by provider (e.g. "anthropic/claude-sonnet-4-6", "openai/gpt-4o"):
//
//	client := openrouter.New("anthropic/claude-sonnet-4-6", openrouter.WithAPIKey(key))
//	resp, err := client.Generate(ctx, gantry.LLMRequest{
//	    Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
//	})
//
// Like OpenAI, OpenRouter carries tool-call arguments as a JSON-encoded string
// and splits them across streamed deltas keyed by index; the client reassembles
// them and preserves the per-call IDs.
package openrouter
