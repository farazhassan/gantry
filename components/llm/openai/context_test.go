package openai_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openai"
)

func userReq() gantry.LLMRequest {
	return gantry.LLMRequest{Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}
}

func TestGenerateReadsCachedTokens(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":130,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":100}}}`)
	})
	resp, err := c.Generate(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := gantry.Usage{InputTokens: 130, OutputTokens: 5, CacheReadTokens: 100}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestContextLengthExceededIsTyped(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"This model's maximum context length is 128000 tokens. However, your messages resulted in 130000 tokens. Please reduce the length of the messages.","type":"invalid_request_error","param":"messages","code":"context_length_exceeded"}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	var cle *gantry.ContextLengthError
	if !errors.As(err, &cle) {
		t.Fatalf("err = %v, want *ContextLengthError", err)
	}
	if cle.Limit != 128000 || cle.Requested != 130000 {
		t.Errorf("cle = %+v, want Limit 128000 Requested 130000", cle)
	}
}

func TestOtherBadRequestStaysGeneric(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"bad","type":"invalid_request_error","code":"invalid_value"}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic error", err)
	}
}

func TestClientDoesNotReportContextWindow(t *testing.T) {
	var c gantry.LLMClient = openai.New("m", openai.WithAPIKey("k"))
	if _, ok := c.(gantry.ContextWindowReporter); ok {
		t.Error("openai.Client implements ContextWindowReporter; OpenAI has no window API, users must set WithContextWindow")
	}
}
