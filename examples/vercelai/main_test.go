package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ui/vercelai"
	"github.com/farazhassan/gantry/eval"
)

func TestParseOriginsTrimsAndFiltersEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"single", "http://localhost:3000", []string{"http://localhost:3000"}},
		{"comma with space", "http://a, http://b", []string{"http://a", "http://b"}},
		{"leading/trailing/double comma", ",http://a,,http://b,", []string{"http://a", "http://b"}},
		{"all whitespace", "   ", nil},
		{"empty", "", nil},
		{"wildcard", "*", []string{"*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseOrigins(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseOrigins(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

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

	body := `{"messages":[{"role":"user","parts":[{"type":"text","text":"Say hi."}]}]}`
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

	for _, want := range []string{`"type":"start"`, `"type":"text-start"`, `"type":"finish"`, "data: [DONE]"} {
		if !strings.Contains(got, want) {
			t.Errorf("SSE stream missing %q\nfull stream:\n%s", want, got)
		}
	}
}

func TestNewHandlerAppliesOptions(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd})

	handler, err := newHandler(llm, vercelai.WithAllowedOrigins("https://example.com"))
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodOptions, srv.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://example.com (opts not forwarded to vercelai.Handler?)", got)
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
		strings.NewReader(`{"messages":[{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]}]}`))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	var b1 strings.Builder
	io.Copy(&b1, r1.Body)
	r1.Body.Close()
	if strings.Contains(b1.String(), `"type":"tool-output-available"`) {
		t.Fatalf("client call should have no result on suspend:\n%s", b1.String())
	}

	// Resume with the answer.
	resume := `{"messages":[` +
		`{"role":"user","parts":[{"type":"text","text":"hi, I am Ada"}]},` +
		`{"role":"assistant","parts":[{"type":"dynamic-tool","toolCallId":"q1","toolName":"ask_user","state":"output-available","input":{"q":"name?"},"output":{"answer":"Ada"}}]}` +
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
