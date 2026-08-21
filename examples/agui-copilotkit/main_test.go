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
	"github.com/farazhassan/gantry/components/ui/agui"
	"github.com/farazhassan/gantry/eval"
)

// TestParseOriginsTrimsAndFiltersEmpty covers AGUI_ALLOWED_ORIGINS parsing: a
// natural value like "http://a, http://b" has a leading space on the second
// entry, and a trailing/duplicate comma is an easy typo -- neither should
// silently produce an origin that can never match a real Origin header.
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

// TestServerStreamsRunToFinish exercises the example's handler wiring with a
// scripted mock LLM, so it stays hermetic with respect to any LLM provider: it
// proves the server emits a well-formed AG-UI SSE stream ending in RUN_FINISHED
// for an ordinary, tool-free turn.
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

// TestNewHandlerAppliesOptions verifies newHandler forwards its opts through
// to agui.Handler -- main() uses this to wire AGUI_ALLOWED_ORIGINS, so a
// regression here would silently break that even though it's invisible from
// main() itself (opts are only observable through the handler's behavior).
func TestNewHandlerAppliesOptions(t *testing.T) {
	llm := eval.NewMockLLMClient(gantry.LLMResponse{Content: "hi", StopReason: gantry.StopReasonEnd})

	handler, err := newHandler(llm, agui.WithAllowedOrigins("https://example.com"))
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
		t.Fatalf("Access-Control-Allow-Origin = %q, want https://example.com (opts not forwarded to agui.Handler?)", got)
	}
}

// TestNewHandlerRequestDeclaredToolSuspendResume proves the whole point of
// this example: a tool declared only in the REQUEST's "tools" field (never
// registered in Go, unlike examples/agui's static ask_user) is advertised to
// the model and, when called, suspends the run for the client (CopilotKit) to
// fulfill -- then a resume POST (replaying history AND re-declaring the same
// tool, since AG-UI requests are stateless) continues the conversation.
func TestNewHandlerRequestDeclaredToolSuspendResume(t *testing.T) {
	mock := eval.NewMockLLMClient(
		gantry.LLMResponse{
			ToolCalls:  []gantry.ToolCall{{ID: "q1", Name: "get_location", Input: json.RawMessage(`{}`)}},
			StopReason: gantry.StopReasonToolUse,
		},
		// Content is deliberately "Lahore..." rather than "You are in Lahore.":
		// eval.MockLLMClient streams text in fixed 6-rune chunks (see
		// eval/mock.go's chunkSize), so a check word not aligned to a chunk
		// boundary can be split across two SSE delta frames and never appear
		// as a contiguous substring in the raw stream text below.
		gantry.LLMResponse{Content: "Lahore is where you are.", StopReason: gantry.StopReasonEnd},
	)
	h, err := newHandler(mock)
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	toolsField := `"tools":[{"name":"get_location","description":"returns the browser's location","parameters":{"type":"object"}}]`

	// Suspend: the model calls get_location, which was never registered in
	// Go -- only declared by this request.
	first := `{"messages":[{"role":"user","content":"where am I?"}],` + toolsField + `}`
	r1, err := http.Post(srv.URL, "application/json", strings.NewReader(first))
	if err != nil {
		t.Fatalf("POST 1: %v", err)
	}
	var b1 strings.Builder
	io.Copy(&b1, r1.Body)
	r1.Body.Close()
	if !strings.Contains(b1.String(), `"type":"TOOL_CALL_START"`) {
		t.Fatalf("missing TOOL_CALL_START:\n%s", b1.String())
	}
	if strings.Contains(b1.String(), `"type":"TOOL_CALL_RESULT"`) {
		t.Fatalf("request-declared call should have no result on suspend:\n%s", b1.String())
	}

	// Resume: a CopilotKit-style resume replays the full history AND resends
	// the tool declaration, since AG-UI requests are stateless.
	resume := `{"messages":[` +
		`{"role":"user","content":"where am I?"},` +
		`{"role":"assistant","toolCalls":[{"id":"q1","type":"function","function":{"name":"get_location","arguments":"{}"}}]},` +
		`{"role":"tool","toolCallId":"q1","content":"{\"lat\":31.5,\"lng\":74.3}"}` +
		`],` + toolsField + `}`
	r2, err := http.Post(srv.URL, "application/json", strings.NewReader(resume))
	if err != nil {
		t.Fatalf("POST 2: %v", err)
	}
	var b2 strings.Builder
	io.Copy(&b2, r2.Body)
	r2.Body.Close()
	if !strings.Contains(b2.String(), "Lahore") {
		t.Fatalf("resume did not finish:\n%s", b2.String())
	}
	if !strings.Contains(b2.String(), "RUN_FINISHED") {
		t.Fatalf("resume did not end in RUN_FINISHED:\n%s", b2.String())
	}
}
