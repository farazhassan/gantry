package openrouter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openrouter"
)

var _ gantry.ContextWindowReporter = (*openrouter.Client)(nil)

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
		_, _ = io.WriteString(w, `{"error":{"message":"This endpoint's maximum context length is 200000 tokens. However, you requested about 250000 tokens (240000 of text input, 10000 in the output). Please reduce the length of either one.","code":400}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	var cle *gantry.ContextLengthError
	if !errors.As(err, &cle) {
		t.Fatalf("err = %v, want *ContextLengthError", err)
	}
	if cle.Limit != 200000 || cle.Requested != 250000 {
		t.Errorf("cle = %+v, want Limit 200000 Requested 250000", cle)
	}
}

func TestOtherBadRequestStaysGeneric(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid model","code":400}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic error", err)
	}
}

func TestContextWindowFromModelsList(t *testing.T) {
	calls := 0
	var gotPath string
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":[{"id":"other","context_length":8000},{"id":"test-model","context_length":200000}]}`)
	})
	for i := 0; i < 2; i++ {
		n, err := c.ContextWindow(context.Background())
		if err != nil || n != 200000 {
			t.Fatalf("ContextWindow = %d, %v; want 200000", n, err)
		}
	}
	if gotPath != "/v1/models" {
		t.Errorf("path = %q, want /v1/models", gotPath)
	}
	if calls != 1 {
		t.Errorf("models called %d times, want 1 (cached)", calls)
	}
}

func TestContextWindowUnknownModel(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"other","context_length":8000}]}`)
	})
	if _, err := c.ContextWindow(context.Background()); err == nil {
		t.Error("ContextWindow: want error for model missing from list")
	}
}
