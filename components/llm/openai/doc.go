// Package openai provides a gantry.StreamingLLMClient backed by
// OpenAI's /v1/chat/completions endpoint. It maps gantry request/response
// types to OpenAI's wire format, supports tool calling, and streams
// Server-Sent Events.
//
// Construct a client with New. The API key comes from WithAPIKey or, failing
// that, the OPENAI_API_KEY environment variable:
//
//	client := openai.New("gpt-4o", openai.WithAPIKey(key))
//	resp, err := client.Generate(ctx, gantry.LLMRequest{
//	    Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
//	})
//
// OpenAI carries tool-call arguments as a JSON-encoded string and splits them
// across streamed deltas keyed by index; the client reassembles them and
// preserves OpenAI's per-call IDs.
package openai
