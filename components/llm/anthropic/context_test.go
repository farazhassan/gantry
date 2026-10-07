package anthropic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/anthropic"
)

var _ gantry.ContextWindowReporter = (*anthropic.Client)(nil)

func userReq() gantry.LLMRequest {
	return gantry.LLMRequest{Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}
}

func TestGenerateNormalizesCacheUsage(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"x"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}`)
	})
	resp, err := c.Generate(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := gantry.Usage{InputTokens: 130, OutputTokens: 5, CacheReadTokens: 100, CacheWriteTokens: 20}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestStreamNormalizesCacheUsage(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n"))
	})
	resp, err := c.GenerateStream(context.Background(), userReq(), func(gantry.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	want := gantry.Usage{InputTokens: 130, OutputTokens: 5, CacheReadTokens: 100, CacheWriteTokens: 20}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestContextWindowExceededStopReason(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"x"}],"stop_reason":"model_context_window_exceeded","usage":{}}`)
	})
	resp, err := c.Generate(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.StopReason != gantry.StopReasonContextWindow {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonContextWindow)
	}
}

func TestPromptTooLongIsContextLengthError(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 210000 tokens > 200000 maximum"}}`)
	})
	for name, call := range map[string]func() error{
		"Generate": func() error { _, err := c.Generate(context.Background(), userReq()); return err },
		"GenerateStream": func() error {
			_, err := c.GenerateStream(context.Background(), userReq(), func(gantry.StreamChunk) error { return nil })
			return err
		},
	} {
		err := call()
		var cle *gantry.ContextLengthError
		if !errors.As(err, &cle) {
			t.Fatalf("%s err = %v, want *ContextLengthError", name, err)
		}
		if cle.Requested != 210000 || cle.Limit != 200000 {
			t.Errorf("%s = %+v, want Requested 210000 Limit 200000", name, cle)
		}
	}
}

func TestOtherBadRequestStaysGeneric(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be positive"}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic non-overflow error", err)
	}
}

func TestContextWindowFromModelsAPIIsCached(t *testing.T) {
	calls := 0
	var gotPath, gotKey string
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-api-key")
		_, _ = io.WriteString(w, `{"id":"test-model","max_input_tokens":1000000,"max_tokens":128000}`)
	})
	for i := 0; i < 2; i++ {
		n, err := c.ContextWindow(context.Background())
		if err != nil {
			t.Fatalf("ContextWindow: %v", err)
		}
		if n != 1000000 {
			t.Errorf("ContextWindow = %d, want 1000000", n)
		}
	}
	if gotPath != "/v1/models/test-model" || gotKey != "test-key" {
		t.Errorf("request path=%q key=%q", gotPath, gotKey)
	}
	if calls != 1 {
		t.Errorf("models API called %d times, want 1 (cached)", calls)
	}
}

func TestContextWindowErrorNotCached(t *testing.T) {
	calls := 0
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"max_input_tokens":500}`)
	})
	if _, err := c.ContextWindow(context.Background()); err == nil {
		t.Fatal("first ContextWindow: want error")
	}
	if n, err := c.ContextWindow(context.Background()); err != nil || n != 500 {
		t.Errorf("second ContextWindow = %d, %v; want 500, nil", n, err)
	}
}
