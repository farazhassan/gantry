package agui

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

// suspendingAgent returns an agent whose mock LLM calls ask_user (a client
// tool) on the first request and answers with text on the second. The handler
// is reused across both POSTs, so the mock's two responses map to run then
// resume.
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

// joinTextDeltas reassembles the text from an SSE stream by concatenating the
// delta fields of every TEXT_MESSAGE_CONTENT frame, in order. The mock chunks
// content into fixed-size rune groups, so the final answer is only visible once
// the deltas are rejoined.
func joinTextDeltas(sse string) string {
	var out strings.Builder
	for _, line := range strings.Split(sse, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[len("data:"):])), &ev); err != nil {
			continue
		}
		if ev.Type == "TEXT_MESSAGE_CONTENT" {
			out.WriteString(ev.Delta)
		}
	}
	return out.String()
}

func TestHandlerSuspendsOnClientTool(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[{"role":"user","content":"hi, I am Ada"}]}`
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
	if !strings.Contains(out, `"type":"TOOL_CALL_START"`) {
		t.Fatalf("missing TOOL_CALL_START:\n%s", out)
	}
	if !strings.Contains(out, `"type":"RUN_FINISHED"`) {
		t.Fatalf("missing RUN_FINISHED:\n%s", out)
	}
	if strings.Contains(out, `"type":"TOOL_CALL_RESULT"`) {
		t.Fatalf("client tool call must have no TOOL_CALL_RESULT:\n%s", out)
	}
}

func TestHandlerResumesOnToolResult(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	first := `{"messages":[{"role":"user","content":"hi, I am Ada"}]}`
	r1, err := http.Post(srv.URL, "application/json", strings.NewReader(first))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	io.Copy(io.Discard, r1.Body)
	r1.Body.Close()

	resume := `{"messages":[` +
		`{"role":"user","content":"hi, I am Ada"},` +
		`{"role":"assistant","toolCalls":[{"id":"q1","type":"function","function":{"name":"ask_user","arguments":"{\"q\":\"name?\"}"}}]},` +
		`{"role":"tool","toolCallId":"q1","content":"{\"answer\":\"Ada\"}"}` +
		`]}`
	r2, err := http.Post(srv.URL, "application/json", strings.NewReader(resume))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	defer r2.Body.Close()
	var sb strings.Builder
	io.Copy(&sb, r2.Body)
	out := sb.String()
	// The mock streams content in fixed-size rune chunks, so the answer is split
	// across several TEXT_MESSAGE_CONTENT frames and never appears contiguously
	// in the raw SSE bytes. Reassemble the deltas before asserting.
	if got := joinTextDeltas(out); !strings.Contains(got, "Hello, Ada!") {
		t.Fatalf("resume did not produce final answer; reassembled %q:\n%s", got, out)
	}
	if !strings.Contains(out, `"type":"RUN_FINISHED"`) {
		t.Fatalf("missing RUN_FINISHED on resume:\n%s", out)
	}
}

func TestHandlerRejectsIncompleteResume(t *testing.T) {
	a := suspendingAgent(t)
	srv := httptest.NewServer(Handler(a))
	t.Cleanup(srv.Close)

	body := `{"messages":[` +
		`{"role":"assistant","toolCalls":[{"id":"q1","type":"function","function":{"name":"ask_user","arguments":"{}"}}]},` +
		`{"role":"assistant","toolCalls":[{"id":"q2","type":"function","function":{"name":"ask_user","arguments":"{}"}}]},` +
		`{"role":"tool","toolCallId":"q1","content":"{}"}` +
		`]}`
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
