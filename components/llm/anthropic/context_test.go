package anthropic_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestContextWindowFailureCachedForTTL(t *testing.T) {
	clock := time.Now()
	anthropic.SetNowForTest(t, func() time.Time { return clock })
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"max_input_tokens":500}`)
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
		_, _ = io.WriteString(w, `{"max_input_tokens":500}`)
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

func TestStreamMessageDeltaCumulativeUsageOverrides(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Join([]string{
			`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100,"cache_creation_input_tokens":20}}}`,
			`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`,
			`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":12,"cache_read_input_tokens":110,"output_tokens":5}}`,
			`data: {"type":"message_stop"}`,
			"",
		}, "\n"))
	})
	resp, err := c.GenerateStream(context.Background(), userReq(), func(gantry.StreamChunk) error { return nil })
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	// input 12 + cache read 110 + cache write 20 (not repeated in delta, kept from start).
	want := gantry.Usage{InputTokens: 142, OutputTokens: 5, CacheReadTokens: 110, CacheWriteTokens: 20}
	if resp.Usage != want {
		t.Errorf("Usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestPromptTooLongWrongErrorTypeStaysGeneric(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"api_error","message":"prompt is too long: 2 tokens > 1 maximum"}}`)
	})
	_, err := c.Generate(context.Background(), userReq())
	if err == nil || errors.Is(err, gantry.ErrContextLengthExceeded) {
		t.Errorf("err = %v, want generic error", err)
	}
}

func TestContextWindowConcurrentSingleRequest(t *testing.T) {
	var calls atomic.Int32
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = io.WriteString(w, `{"max_input_tokens":1000}`)
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
		t.Errorf("models API called %d times, want 1", got)
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
		_, _ = io.WriteString(w, `{"max_input_tokens":1000}`)
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

func TestContextWindowStopReasonBeatsToolUse(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","id":"t1","name":"search","input":{"q":"x"}}],
			"stop_reason":"model_context_window_exceeded","usage":{}}`)
	})
	resp, err := c.Generate(context.Background(), userReq())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.StopReason != gantry.StopReasonContextWindow {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonContextWindow)
	}
}
