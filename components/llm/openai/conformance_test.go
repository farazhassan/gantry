package openai_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/conformance"
)

// conformanceHandler answers /v1/chat/completions for both stream=false (single
// JSON object) and stream=true (SSE), always with a valid "stop" reply so the
// conformance assertions (non-empty StopReason, delta/content parity) hold.
func conformanceHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Stream {
		sse(w,
			`{"choices":[{"delta":{"role":"assistant","content":"hello world"}}]}`,
			`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			`{"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":2}}`,
			"[DONE]",
		)
		return
	}
	_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":2}}`)
}

func TestOpenAIConformsToLLMClient(t *testing.T) {
	conformance.LLMClientSuite(t, func() gantry.LLMClient {
		return newServerClient(t, conformanceHandler)
	})
}

func TestOpenAIConformsToStreamingLLMClient(t *testing.T) {
	conformance.StreamingLLMClientSuite(t, func() gantry.StreamingLLMClient {
		return newServerClient(t, conformanceHandler)
	})
}
