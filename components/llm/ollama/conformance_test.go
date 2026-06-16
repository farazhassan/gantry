package ollama_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/conformance"
)

// conformanceHandler answers /api/chat for both stream=false (single JSON
// object) and stream=true (NDJSON), always with a valid "stop" reply so the
// conformance assertions (non-empty StopReason, delta/content parity) hold.
func conformanceHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Stream {
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"hello world"},"done":false}`+"\n")
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":2}`+"\n")
		return
	}
	_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"hello world"},"done":true,"done_reason":"stop","prompt_eval_count":2,"eval_count":2}`)
}

func TestOllamaConformsToLLMClient(t *testing.T) {
	conformance.LLMClientSuite(t, func() gantry.LLMClient {
		return newServerClient(t, conformanceHandler)
	})
}

func TestOllamaConformsToStreamingLLMClient(t *testing.T) {
	conformance.StreamingLLMClientSuite(t, func() gantry.StreamingLLMClient {
		return newServerClient(t, conformanceHandler)
	})
}
