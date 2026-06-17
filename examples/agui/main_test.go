package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/eval"
)

// TestServerStreamsRunToFinish exercises the example's handler wiring with a
// scripted mock LLM, so it stays hermetic with respect to any LLM provider: it
// proves the server emits a well-formed AG-UI SSE stream ending in RUN_FINISHED.
func TestServerStreamsRunToFinish(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{
		Content:    "Hi there friend.",
		StopReason: gantry.StopReasonEnd,
	})

	handler, err := newHandler(llm)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}

	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"messages":[{"role":"user","content":"Say hi."}]}`
	resp, err := srv.Client().Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	var sb strings.Builder
	if _, err := io.Copy(&sb, resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	got := sb.String()

	for _, want := range []string{"RUN_STARTED", "TEXT_MESSAGE_START", "RUN_FINISHED"} {
		if !strings.Contains(got, want) {
			t.Errorf("SSE stream missing %q\nfull stream:\n%s", want, got)
		}
	}
}

func TestNewHandlerAskUserSuspendResume(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "ask_user", Input: json.RawMessage(`{"q":"name?"}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		gantry.LLMResponse{Content: "Hello, Ada!", StopReason: gantry.StopReasonEnd},
	)
	h, err := newHandler(mock)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// Suspend.
	r1, err := http.Post(srv.URL, "application/json",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi, I am Ada"}]}`))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	var b1 strings.Builder
	io.Copy(&b1, r1.Body)
	r1.Body.Close()
	if strings.Contains(b1.String(), `"type":"TOOL_CALL_RESULT"`) {
		t.Fatalf("client call should have no result on suspend:\n%s", b1.String())
	}

	// Resume with the answer.
	resume := `{"messages":[` +
		`{"role":"user","content":"hi, I am Ada"},` +
		`{"role":"assistant","toolCalls":[{"id":"q1","type":"function","function":{"name":"ask_user","arguments":"{\"q\":\"name?\"}"}}]},` +
		`{"role":"tool","toolCallId":"q1","content":"{\"answer\":\"Ada\"}"}` +
		`]}`
	r2, err := http.Post(srv.URL, "application/json", strings.NewReader(resume))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	var b2 strings.Builder
	io.Copy(&b2, r2.Body)
	r2.Body.Close()
	if !strings.Contains(b2.String(), "Ada") {
		t.Fatalf("resume did not finish:\n%s", b2.String())
	}
}
