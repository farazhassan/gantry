package agui

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/farazhassan/gantry"
)

// blockingLLM never returns from Generate until unblock is closed (or ctx is
// cancelled), so tests can hold a run open long enough to observe behavior
// that only shows up while the agent is still "thinking" — e.g. heartbeat
// pings between real events.
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

// panickingLLM panics with an internal-looking detail so tests can assert
// that detail never reaches the client verbatim (a raw recovered panic value
// is exactly the kind of thing that can carry file paths, request internals,
// or other sensitive detail).
type panickingLLM struct{}

func (panickingLLM) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	panic("panickingLLM: sensitive internal detail")
}

// TestHandlerWithMaxBodyBytesOverride verifies WithMaxBodyBytes lets a caller
// tighten (or loosen) the request-body cap independently of the package
// default, since 1 MiB may not fit every deployment's history size.
func TestHandlerWithMaxBodyBytesOverride(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithMaxBodyBytes(64)))
	t.Cleanup(srv.Close)

	// Well under the package default (1 MiB) but over the 64-byte override.
	body := `{"messages":[{"role":"user","content":"` + strings.Repeat("a", 128) + `"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body exceeds the 64-byte override)", resp.StatusCode)
	}
}

// TestHandlerSendsHeartbeatPingsWhileRunIsBlocked holds the LLM call open
// with blockingLLM and asserts the connection carries SSE keep-alive
// comments in the meantime, then that the stream still completes normally
// once the call is allowed to return. This is the proxy/load-balancer
// idle-timeout scenario: no real event to send, but bytes must still cross
// the wire periodically.
func TestHandlerSendsHeartbeatPingsWhileRunIsBlocked(t *testing.T) {
	unblock := make(chan struct{})
	llm := &blockingLLM{unblock: unblock, resp: gantry.LLMResponse{Content: "done", StopReason: gantry.StopReasonEnd}}
	a, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}

	srv := httptest.NewServer(Handler(a, WithHeartbeatInterval(5*time.Millisecond)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
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

	close(unblock) // let the LLM call return so the run can finish
	var rest strings.Builder
	for sc.Scan() {
		rest.WriteString(sc.Text())
		rest.WriteString("\n")
	}
	if !strings.Contains(rest.String(), "RUN_FINISHED") {
		t.Fatalf("missing RUN_FINISHED after unblock:\n%s", rest.String())
	}
}

// TestHandlerRecoversPanicAsRunError verifies a panic inside the LLM/tool
// call path (after SSE headers are already committed) is recovered into a
// clean RUN_ERROR frame instead of killing the connection with no signal to
// the client, and that the recovered panic value's detail is NOT forwarded
// verbatim -- only a generic message is client-visible.
func TestHandlerRecoversPanicAsRunError(t *testing.T) {
	a, err := gantry.NewAgent(gantry.WithLLM(panickingLLM{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
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
	if !strings.Contains(out, `"type":"RUN_ERROR"`) {
		t.Fatalf("missing RUN_ERROR after panic:\n%s", out)
	}
	if strings.Contains(out, "sensitive internal detail") {
		t.Fatalf("panic detail leaked to client:\n%s", out)
	}
}

// TestHandlerLogsRunErrorServerSide verifies a run's terminal error is
// logged server-side (at ERROR level), not just streamed to a client that
// may already be gone -- see WithLogger.
func TestHandlerLogsRunErrorServerSide(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a, WithLogger(logger)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
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

// TestHandlerLogsPanicDetailServerSide verifies the full panic value and
// stack ARE preserved server-side via the logger, even though (per
// TestHandlerRecoversPanicAsRunError) they must never reach the client.
func TestHandlerLogsPanicDetailServerSide(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	a, err := gantry.NewAgent(gantry.WithLLM(panickingLLM{}))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	srv := httptest.NewServer(Handler(a, WithLogger(logger)))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	logs := buf.String()
	if !strings.Contains(logs, "sensitive internal detail") {
		t.Fatalf("expected the panic detail to be logged server-side, got:\n%s", logs)
	}
	if !strings.Contains(logs, "level=ERROR") {
		t.Fatalf("expected ERROR level, got:\n%s", logs)
	}
}

// TestRunErrorLogLevelTreatsCancellationAsDebug verifies the log-level
// policy directly: a client disconnect (context.Canceled) is expected
// traffic, not a failure, and logging it at ERROR would be alert-fatigue
// noise in production. Any other error is a real failure and logs at ERROR.
func TestRunErrorLogLevelTreatsCancellationAsDebug(t *testing.T) {
	if got := runErrorLogLevel(context.Canceled); got != slog.LevelDebug {
		t.Errorf("runErrorLogLevel(context.Canceled) = %v, want Debug", got)
	}
	wrapped := fmt.Errorf("run: %w", context.Canceled)
	if got := runErrorLogLevel(wrapped); got != slog.LevelDebug {
		t.Errorf("runErrorLogLevel(wrapped context.Canceled) = %v, want Debug", got)
	}
	if got := runErrorLogLevel(errors.New("llm boom")); got != slog.LevelError {
		t.Errorf("runErrorLogLevel(ordinary error) = %v, want Error", got)
	}
}

// TestHandlerAppliesErrorMapperToClientMessage verifies WithErrorMapper lets
// a caller redact/rewrite the client-visible RUN_ERROR message independently
// of what's logged server-side -- forwarding err.Error() verbatim by default
// risks leaking upstream response bodies, internal URLs, etc.
func TestHandlerAppliesErrorMapperToClientMessage(t *testing.T) {
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a, WithErrorMapper(func(error) string {
		return "something went wrong"
	})))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
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
		t.Fatalf("mapped message missing from RUN_ERROR:\n%s", out)
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

// TestHandlerCORSDisabledByDefault verifies the zero-arg Handler(agent) is
// unaffected by the CORS feature: OPTIONS still 405s exactly as before
// WithAllowedOrigins existed, and no Access-Control-* headers appear.
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

// TestHandlerCORSPreflightAllowsConfiguredOrigin verifies a listed origin
// gets a real preflight response: 204, matching Allow-Origin, and the
// method/headers a browser needs to permit the actual POST that follows.
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
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("Access-Control-Allow-Methods = %q, want it to contain POST", got)
	}
	if resp.Header.Get("Vary") != "Origin" {
		t.Fatalf("Vary = %q, want Origin (explicit allow-list, not wildcard)", resp.Header.Get("Vary"))
	}
}

// TestHandlerCORSPreflightRejectsUnlistedOrigin verifies an origin NOT in
// the allow-list gets no Access-Control-Allow-Origin header, so the browser
// blocks the follow-up request client-side even though the preflight itself
// doesn't hard-fail.
func TestHandlerCORSPreflightRejectsUnlistedOrigin(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithAllowedOrigins("https://example.com")))
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Do(newCORSPreflightRequest(t, srv.URL, "https://evil.example"))
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want unset for an unlisted origin", got)
	}
}

// TestHandlerCORSSetsHeaderOnActualResponse verifies the ACTUAL POST/SSE
// response (not just the preflight) carries Access-Control-Allow-Origin —
// browsers enforce CORS on the real response too, and it's easy to wire up
// only the preflight and have the real request still get blocked.
func TestHandlerCORSSetsHeaderOnActualResponse(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithAllowedOrigins("https://example.com")))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://example.com", got)
	}
}

// TestHandlerCORSWildcardAllowsAnyOrigin verifies WithAllowedOrigins("*")
// echoes "*" (not the request's Origin) and skips Vary: Origin, since the
// response doesn't actually vary per origin in that case.
func TestHandlerCORSWildcardAllowsAnyOrigin(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a, WithAllowedOrigins("*")))
	t.Cleanup(srv.Close)

	resp, err := http.DefaultClient.Do(newCORSPreflightRequest(t, srv.URL, "https://anything.example"))
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := resp.Header.Get("Vary"); got != "" {
		t.Fatalf("Vary = %q, want unset for wildcard", got)
	}
}
