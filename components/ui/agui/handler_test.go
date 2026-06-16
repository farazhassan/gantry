package agui

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

func newTestAgent(t *testing.T, resp gantry.LLMResponse) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(resp)))
	if err != nil {
		t.Fatalf("gantry.New: %v", err)
	}
	return a
}

type erroringLLM struct{}

func (erroringLLM) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{}, errors.New("llm boom")
}

func newErroringAgent(t *testing.T) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(erroringLLM{}))
	if err != nil {
		t.Fatalf("gantry.New: %v", err)
	}
	return a
}

func TestHandlerStreamsRunToFinish(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "Hello!", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"threadId":"t1","runId":"r1","messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, `"type":"RUN_STARTED"`) {
		t.Fatalf("missing RUN_STARTED:\n%s", out)
	}
	if !strings.Contains(out, `"type":"RUN_FINISHED"`) {
		t.Fatalf("missing RUN_FINISHED:\n%s", out)
	}
}

func TestHandlerBadRequest(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	// Last message is not a user turn -> 400 before any SSE.
	body := `{"messages":[{"role":"assistant","content":"hi"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerRejectsOversizedBody(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	// A body larger than maxRequestBytes must be rejected before any SSE.
	big := strings.Repeat("a", maxRequestBytes+1)
	body := `{"messages":[{"role":"user","content":"` + big + `"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHandlerMidStreamError(t *testing.T) {
	// A mock LLM that returns an error makes RunFromStream fail after headers
	// are sent, so the handler must emit a RUN_ERROR frame.
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi"}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	_, _ = io.Copy(&sb, resp.Body)
	if !strings.Contains(sb.String(), `"type":"RUN_ERROR"`) {
		t.Fatalf("missing RUN_ERROR:\n%s", sb.String())
	}
}
