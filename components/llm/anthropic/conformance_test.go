package anthropic_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/conformance"
)

// conformanceHandler answers /v1/messages for both stream=false (single JSON
// object) and stream=true (SSE), always with a valid "end_turn" reply so the
// conformance assertions (non-empty StopReason, delta/content parity) hold.
func conformanceHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Stream {
		sse(w, [][2]string{
			{"message_start", `{"type":"message_start","message":{"usage":{"input_tokens":2,"output_tokens":0}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello world"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		})
		return
	}
	_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"hello world"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":2}}`)
}

func TestAnthropicConformsToLLMClient(t *testing.T) {
	conformance.LLMClientSuite(t, func() gantry.LLMClient {
		return newServerClient(t, conformanceHandler)
	})
}

func TestAnthropicConformsToStreamingLLMClient(t *testing.T) {
	conformance.StreamingLLMClientSuite(t, func() gantry.StreamingLLMClient {
		return newServerClient(t, conformanceHandler)
	})
}
