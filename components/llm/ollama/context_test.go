package ollama_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/ollama"
)

var _ gantry.ContextWindowReporter = (*ollama.Client)(nil)

func TestWithNumCtxSentAndReported(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"x"},"done":true,"done_reason":"stop"}`)
	}))
	t.Cleanup(ts.Close)

	c := ollama.New("test-model", ollama.WithNumCtx(32768), ollama.WithBaseURL(ts.URL), ollama.WithHTTPClient(ts.Client()))
	if _, err := c.Generate(context.Background(), gantry.LLMRequest{Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	opts, _ := gotBody["options"].(map[string]any)
	if opts["num_ctx"] != float64(32768) {
		t.Errorf("options.num_ctx = %v, want 32768", opts["num_ctx"])
	}
	if n, err := c.ContextWindow(context.Background()); err != nil || n != 32768 {
		t.Errorf("ContextWindow = %d, %v; want 32768", n, err)
	}
}

func TestContextWindowUnknownWithoutNumCtx(t *testing.T) {
	c := ollama.New("m")
	if n, err := c.ContextWindow(context.Background()); n != 0 || err != nil {
		t.Errorf("ContextWindow without WithNumCtx = %d, %v; want 0, nil (unknown)", n, err)
	}
}

func TestWithNumCtxSentOnStreamingPath(t *testing.T) {
	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"message":{"role":"assistant","content":"x"},"done":true,"done_reason":"stop"}`+"\n")
	}))
	t.Cleanup(ts.Close)

	c := ollama.New("test-model", ollama.WithNumCtx(16384), ollama.WithBaseURL(ts.URL), ollama.WithHTTPClient(ts.Client()))
	req := gantry.LLMRequest{Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hi"}}}
	if _, err := c.GenerateStream(context.Background(), req, func(gantry.StreamChunk) error { return nil }); err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}
	if gotBody["stream"] != true {
		t.Errorf("stream = %v, want true", gotBody["stream"])
	}
	opts, _ := gotBody["options"].(map[string]any)
	if opts["num_ctx"] != float64(16384) {
		t.Errorf("options.num_ctx = %v, want 16384", opts["num_ctx"])
	}
}
