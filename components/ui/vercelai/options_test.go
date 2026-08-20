package vercelai

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
)

type blockingLLM struct {
	unblock <-chan struct{}
	resp    gantry.LLMResponse
}

func (b *blockingLLM) Generate(ctx context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	select {
	case <-b.unblock:
		return b.resp, nil
	case <-ctx.Done():
		return gantry.LLMResponse{}, ctx.Err()
	}
}

type panickingLLM struct{}

func (panickingLLM) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	panic("panickingLLM: sensitive internal detail")
}

func TestHandlerWithMaxBodyBytesOverride(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithMaxBodyBytes(64)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"` + strings.Repeat("a", 128) + `"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body exceeds the 64-byte override)", resp.StatusCode)
	}
}

func TestHandlerSendsHeartbeatPingsWhileRunIsBlocked(t *testing.T) {
	unblock := make(chan struct{})
	llm := &blockingLLM{unblock: unblock, resp: gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd}}
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv := httptest.NewServer(Handler(a, WithHeartbeatInterval(5*time.Millisecond)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	pings := 0
	for pings < 2 && sc.Scan() {
		if sc.Text() == ": ping" {
			pings++
		}
	}
	if pings < 2 {
		t.Fatalf("expected at least 2 heartbeat pings while the run was blocked, got %d (scan err: %v)", pings, sc.Err())
	}

	close(unblock)
	var rest strings.Builder
	for sc.Scan() {
		rest.WriteString(sc.Text())
		rest.WriteString("\n")
	}
	if !strings.Contains(rest.String(), `"type":"finish"`) {
		t.Fatalf("missing finish after unblock:\n%s", rest.String())
	}
}

func TestHandlerRecoversPanicAsErrorChunk(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(panickingLLM{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var sb strings.Builder
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		sb.WriteString(sc.Text())
		sb.WriteString("\n")
	}
	out := sb.String()
	if !strings.Contains(out, `"type":"error"`) {
		t.Fatalf("missing error chunk after panic:\n%s", out)
	}
	if strings.Contains(out, "sensitive internal detail") {
		t.Fatalf("panic detail leaked to client:\n%s", out)
	}
}

func TestHandlerLogsRunErrorServerSide(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a, WithLogger(logger)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, "llm boom") {
		t.Fatalf("expected the run error to be logged server-side, got:\n%s", logs)
	}
	if !strings.Contains(logs, "level=ERROR") {
		t.Fatalf("expected ERROR level, got:\n%s", logs)
	}
}

func TestHandlerAppliesErrorMapperToClientMessage(t *testing.T) {
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a, WithErrorMapper(func(error) string {
		return "something went wrong"
	})))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	io.Copy(&sb, resp.Body)
	out := sb.String()

	if strings.Contains(out, "llm boom") {
		t.Fatalf("raw error leaked to client despite WithErrorMapper:\n%s", out)
	}
	if !strings.Contains(out, "something went wrong") {
		t.Fatalf("mapped message missing from error chunk:\n%s", out)
	}
}

func newCORSPreflightRequest(t *testing.T, url, origin string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodOptions, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", "POST")
	return req
}

func TestHandlerCORSDisabledByDefault(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Do(newCORSPreflightRequest(t, srv.URL, "https://example.com"))
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405 (CORS not configured)", resp.StatusCode)
	}
	if h := resp.Header.Get("Access-Control-Allow-Origin"); h != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want unset", h)
	}
}

func TestHandlerCORSPreflightAllowsConfiguredOrigin(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithAllowedOrigins("https://example.com")))
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Do(newCORSPreflightRequest(t, srv.URL, "https://example.com"))
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://example.com", got)
	}
}
