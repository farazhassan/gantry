// Package anthropic provides a gantry.StreamingLLMClient backed by
// Anthropic's /v1/messages endpoint. It maps gantry request/response types to
// Anthropic's content-block wire format, supports tool calling, and streams
// Server-Sent Events.
//
// Construct a client with New. The API key comes from WithAPIKey or, failing
// that, the ANTHROPIC_API_KEY environment variable:
//
//	client := anthropic.New("claude-sonnet-4-6", anthropic.WithAPIKey(key))
//	resp, err := client.Generate(ctx, gantry.LLMRequest{
//	    Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}},
//	})
//
// Anthropic carries the system prompt as a top-level field, models messages as
// content blocks, and requires a positive max_tokens (the adapter supplies a
// default when the request leaves it at 0). Tool results are mapped to
// user-role tool_result blocks, and streamed tool inputs are reassembled from
// input_json_delta fragments.
package anthropic
