package vercelai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/ui/internal/streamconfig"
	"github.com/farazhassan/gantry/eval"
)

func newTestAgent(t *testing.T, resp gantry.LLMResponse) *gantry.Agent {
	t.Helper()
	a, err := gantry.NewAgent(gantry.WithLLM(eval.NewMockLLMClient(resp)))
	if err != nil {
		t.Fatalf("gantry.NewAgent: %v", err)
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
		t.Fatalf("gantry.NewAgent: %v", err)
	}
	return a
}

func TestHandlerStreamsRunToFinish(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "Hello!", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"id":"chat1","messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	if h := resp.Header.Get("x-vercel-ai-ui-message-stream"); h != "v1" {
		t.Fatalf("x-vercel-ai-ui-message-stream = %q, want v1", h)
	}
	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	out := sb.String()
	for _, want := range []string{`"type":"start"`, `"type":"text-start"`, `"type":"finish"`, "data: [DONE]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestHandlerBadRequest(t *testing.T) {
	a := newTestAgent(t, gantry.LLMResponse{Content: "x", StopReason: gantry.StopReasonEnd})
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	// Last message is not a user turn and has no tool call -> 400 before any SSE.
	body := `{"messages":[{"role":"assistant","parts":[{"type":"text","text":"hi"}]}]}`
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

	big := strings.Repeat("a", int(streamconfig.DefaultMaxBodyBytes)+1)
	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"` + big + `"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func suspendingAgent(t *testing.T) *gantry.Agent {
	t.Helper()
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "Hello, Ada!", StopReason: gantry.StopReasonEnd},
	)
	a, err := gantry.NewAgent(gantry.WithLLM(mock))
	if err != nil {
		t.Fatalf("NewAgent: %v", err)
	}
	if err := a.With(tool.Client(ask.Definition())); err != nil {
		t.Fatalf("install client tools: %v", err)
	}
	return a
}

// joinTextDeltas reassembles the text from an SSE stream by concatenating
// the delta fields of every text-delta chunk, in order. The mock chunks
// content into fixed-size rune groups, so the final answer is only visible
// once the deltas are rejoined.
func joinTextDeltas(sse string) string {
	var out strings.Builder
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") || strings.Contains(line, "[DONE]") {
			continue
		}
		var c struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[len("data:"):])), &c); err != nil {
			continue
		}
		if c.Type == "text-delta" {
			out.WriteString(c.Delta)
		}
	}
	return out.String()
}

func TestHandlerSuspendsOnClientTool(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
	out := sb.String()
	if !strings.Contains(out, `"type":"tool-input-start"`) {
		t.Fatalf("missing tool-input-start:\n%s", out)
	}
	if !strings.Contains(out, `"type":"finish"`) {
		t.Fatalf("missing finish:\n%s", out)
	}
	if strings.Contains(out, `"type":"tool-output-available"`) {
		t.Fatalf("client tool call must have no tool-output-available:\n%s", out)
	}
}

func TestHandlerResumesOnToolResult(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	first := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]}]}`
	r1, err := http.Post(srv.URL, "application/json", strings.NewReader(first))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	io.Copy(io.Discard, r1.Body)
	r1.Body.Close()

	resume := `{"messages":[` +
		`{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]},` +
		`{"role":"assistant","parts":[{"type":"dynamic-tool","toolCallId":"q1","toolName":"ask_user","state":"output-available","input":{"q":"name?"},"output":{"answer":"Ada"}}]}` +
		`]}`
	r2, err := http.Post(srv.URL, "application/json", strings.NewReader(resume))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	defer r2.Body.Close()
	var sb strings.Builder
	io.Copy(&sb, r2.Body)
	out := sb.String()
	if got := joinTextDeltas(out); !strings.Contains(got, "Hello, Ada!") {
		t.Fatalf("resume did not produce final answer; reassembled %q:\n%s", got, out)
	}
	if !strings.Contains(out, `"type":"finish"`) {
		t.Fatalf("missing finish on resume:\n%s", out)
	}
}

func TestHandlerRejectsIncompleteResume(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"assistant","parts":[` +
		`{"type":"dynamic-tool","toolCallId":"q1","toolName":"ask_user","state":"output-available","input":{},"output":{}},` +
		`{"type":"dynamic-tool","toolCallId":"q2","toolName":"ask_user","state":"input-available","input":{}}` +
		`]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (q2 has no result)", resp.StatusCode)
	}
}

func TestHandlerMidStreamError(t *testing.T) {
	a := newErroringAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi"}]}]}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	_, _ = io.Copy(&sb, resp.Body)
	out := sb.String()
	if !strings.Contains(out, `"type":"error"`) {
		t.Fatalf("missing error chunk:\n%s", out)
	}
	if !strings.Contains(out, "data: [DONE]") {
		t.Fatalf("missing terminal [DONE]:\n%s", out)
	}
}

func TestHandlerResumeOmitsFreshMessageID(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	first := `{"messages":[{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]}]}`
	r1, err := http.Post(srv.URL, "application/json", strings.NewReader(first))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	io.Copy(io.Discard, r1.Body)
	r1.Body.Close()

	resume := `{"messages":[` +
		`{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]},` +
		`{"role":"assistant","parts":[{"type":"dynamic-tool","toolCallId":"q1","toolName":"ask_user","state":"output-available","input":{"q":"name?"},"output":{"answer":"Ada"}}]}` +
		`]}`
	r2, err := http.Post(srv.URL, "application/json", strings.NewReader(resume))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	defer r2.Body.Close()
	var sb strings.Builder
	io.Copy(&sb, r2.Body)
	out := sb.String()

	// Find the "start" chunk and confirm it carries no messageId -- the
	// resume response continues the client's existing assistant message,
	// so minting a fresh id here would silently re-identify it client-side.
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") || !strings.Contains(line, `"type":"start"`) {
			continue
		}
		if strings.Contains(line, "messageId") {
			t.Fatalf("resume's start chunk carries a messageId, want it omitted:\n%s", line)
		}
		return
	}
	t.Fatalf("no start chunk found in resume response:\n%s", out)
}

func TestRunErrorLogLevelTreatsCancellationAsDebug(t *testing.T) {
	if got := runErrorLogLevel(context.Canceled); got.String() != "DEBUG" {
		t.Errorf("runErrorLogLevel(context.Canceled) = %v, want Debug", got)
	}
	if got := runErrorLogLevel(errors.New("llm boom")); got.String() != "ERROR" {
		t.Errorf("runErrorLogLevel(ordinary error) = %v, want Error", got)
	}
}
