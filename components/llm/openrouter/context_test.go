package openrouter_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func badRequest(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, body)
	}
}

func TestOverflowViaMetadataErrorType(t *testing.T) {
	c := newServerClient(t, badRequest(`{"error":{"message":"Provider returned error","code":400,"metadata":{"error_type":"context_length_exceeded","provider_name":"X"}}}`))
	_, err := c.Generate(context.Background(), userReq())
	var cle *gantry.ContextLengthError
	if !errors.As(err, &cle) {
		t.Fatalf("err = %v, want *ContextLengthError", err)
	}
}

func TestOverflowViaNestedAnthropicRaw(t *testing.T) {
	c := newServerClient(t, badRequest(`{"error":{"message":"Provider returned error","code":400,"metadata":{"raw":"{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"prompt is too long: 210000 tokens > 200000 maximum\"}}","provider_name":"Anthropic"}}}`))
	_, err := c.Generate(context.Background(), userReq())
	var cle *gantry.ContextLengthError
	if !errors.As(err, &cle) {
		t.Fatalf("err = %v, want *ContextLengthError", err)
	}
	if cle.Requested != 210000 || cle.Limit != 200000 {
		t.Errorf("cle = %+v, want Requested 210000 Limit 200000", cle)
	}
}

func TestOverflowAnthropicWordingInMessage(t *testing.T) {
	c := newServerClient(t, badRequest(`{"error":{"message":"prompt is too long: 9 tokens > 5 maximum","code":400}}`))
	_, err := c.Generate(context.Background(), userReq())
	if !errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want overflow", err)
	}
}

func TestNonStringRawIgnored(t *testing.T) {
	c := newServerClient(t, badRequest(`{"error":{"message":"Provider returned error","code":400,"metadata":{"raw":{"a":1}}}}`))
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic", err)
	}
}

func TestMaxTokensExceededStaysGeneric(t *testing.T) {
	for _, typ := range []string{"max_tokens_exceeded", "token_limit_exceeded"} {
		c := newServerClient(t, badRequest(`{"error":{"message":"Provider returned error","code":400,"metadata":{"error_type":"`+typ+`"}}}`))
		_, err := c.Generate(context.Background(), userReq())
		if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
			t.Errorf("%s: err = %v, want generic", typ, err)
		}
	}
}

func TestOverflowOnlyOn400(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"maximum context length is 5 tokens"}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic", err)
	}
}

func TestOverflowCommaGroupedAndLimitOfWording(t *testing.T) {
	c := newServerClient(t, badRequest(`{"error":{"message":"Limit of 272,000 tokens; you requested 300,000 tokens.","code":400}}`))
	_, err := c.Generate(context.Background(), userReq())
	var cle *gantry.ContextLengthError
	if !errors.As(err, &cle) || cle.Limit != 272000 || cle.Requested != 300000 {
		t.Errorf("err = %v (%+v), want Limit 272000 Requested 300000", err, cle)
	}
}

func TestContextWindowStripsSuffix(t *testing.T) {
	srvClient := newServerClientModel(t, "test-model:nitro", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":4242}]}`)
	})
	if n, err := srvClient.ContextWindow(context.Background()); err != nil || n != 4242 {
		t.Errorf("ContextWindow = %d, %v; want 4242", n, err)
	}
}

func TestContextWindowExactIDPreferredOverStripped(t *testing.T) {
	c := newServerClientModel(t, "test-model:free", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":100},{"id":"test-model:free","context_length":200}]}`)
	})
	if n, err := c.ContextWindow(context.Background()); err != nil || n != 200 {
		t.Errorf("ContextWindow = %d, %v; want 200", n, err)
	}
}

func newServerClientModel(t *testing.T, model string, handler http.HandlerFunc) *openrouter.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return openrouter.New(model,
		openrouter.WithAPIKey("test-key"),
		openrouter.WithBaseURL(srv.URL),
		openrouter.WithHTTPClient(srv.Client()),
	)
}

func TestContextWindowFailureCachedForTTL(t *testing.T) {
	clock := time.Now()
	openrouter.SetNowForTest(t, func() time.Time { return clock })
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":500}]}`)
	})
	_, err1 := c.ContextWindow(context.Background())
	if err1 == nil {
		t.Fatal("first ContextWindow: want error")
	}
	if _, err := c.ContextWindow(context.Background()); err == nil || err.Error() != err1.Error() || calls.Load() != 1 {
		t.Fatalf("second ContextWindow within TTL: err=%v calls=%d; want cached failure and 1 call", err, calls.Load())
	}
	clock = clock.Add(6 * time.Minute)
	if n, err := c.ContextWindow(context.Background()); err != nil || n != 500 {
		t.Errorf("after TTL ContextWindow = %d, %v; want 500, nil", n, err)
	}
}

func TestContextWindowCancelledCallerNotCached(t *testing.T) {
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":500}]}`)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.ContextWindow(ctx); err == nil {
		t.Fatal("cancelled ContextWindow: want error")
	}
	if n, err := c.ContextWindow(context.Background()); err != nil || n != 500 {
		t.Errorf("ContextWindow after caller cancel = %d, %v; want 500, nil (not cached)", n, err)
	}
}

func TestContextWindowConcurrentSingleRequest(t *testing.T) {
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":1000}]}`)
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if n, err := c.ContextWindow(context.Background()); err != nil || n != 1000 {
				t.Errorf("ContextWindow = %d, %v", n, err)
			}
		}()
	}
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Errorf("models called %d times, want 1", got)
	}
}

func TestContextWindowWaiterHonoursContext(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model","context_length":1000}]}`)
	})
	first := make(chan error, 1)
	go func() { _, err := c.ContextWindow(context.Background()); first <- err }()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, err := c.ContextWindow(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiter err = %v, want deadline exceeded", err)
	}
	if time.Since(begin) > time.Second {
		t.Errorf("waiter blocked %v behind the in-flight lookup", time.Since(begin))
	}
	close(release)
	if err := <-first; err != nil {
		t.Errorf("first call: %v", err)
	}
}
