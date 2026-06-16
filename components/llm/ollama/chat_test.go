package ollama_test

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
	"github.com/farazhassan/gantry/components/llm/ollama"
)

// newServerClient spins up an httptest server with the given handler and returns
// a Client pointed at it. The server is closed via t.Cleanup.
func newServerClient(t *testing.T, handler http.HandlerFunc) *ollama.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return ollama.New("test-model", ollama.WithBaseURL(srv.URL), ollama.WithHTTPClient(srv.Client()))
}

// decodeJSON reads the request body into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func TestGenerateMapsRequestAndResponse(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{
			"message": {"role":"assistant","content":"hi there"},
			"done": true, "done_reason": "stop",
			"prompt_eval_count": 11, "eval_count": 7
		}`)
	})

	resp, err := c.Generate(context.Background(), gantry.LLMRequest{
		System:   "be brief",
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// --- request shape ---
	if gotPath != "/api/chat" {
		t.Errorf("path = %q, want /api/chat", gotPath)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", gotBody["model"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2 (system + user)", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "be brief" {
		t.Errorf("first message = %v, want system/be brief", first)
	}

	// --- response mapping ---
	if resp.Content != "hi there" {
		t.Errorf("Content = %q, want %q", resp.Content, "hi there")
	}
	if resp.StopReason != gantry.StopReasonEnd {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonEnd)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want In=11 Out=7", resp.Usage)
	}
}

func TestGenerateMaxTokensDoneReasonMapsToMaxTokens(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"trunc"},"done":true,"done_reason":"length"}`)
	})
	resp, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.StopReason != gantry.StopReasonMaxTokens {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonMaxTokens)
	}
}

func TestGenerateMapsToolCallsWithSynthesizedIDs(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{
			"message": {"role":"assistant","content":"",
				"tool_calls":[{"function":{"name":"get_weather","arguments":{"city":"SF"}}}]},
			"done": true, "done_reason": "stop"
		}`)
	})

	resp, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "weather?"}},
		Tools: []gantry.ToolDef{{
			Name:        "get_weather",
			Description: "look up weather",
			Schema:      json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Tools forwarded in OpenAI-style function wrapper.
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool0["type"])
	}

	// Response tool calls: synthesized ID, name, raw arguments preserved.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call-0" {
		t.Errorf("ToolCall.ID = %q, want call-0", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want get_weather", tc.Name)
	}
	if strings.TrimSpace(string(tc.Input)) != `{"city":"SF"}` {
		t.Errorf("ToolCall.Input = %s, want {\"city\":\"SF\"}", tc.Input)
	}
	if resp.StopReason != gantry.StopReasonToolUse {
		t.Errorf("StopReason = %q, want %q (tool calls present)", resp.StopReason, gantry.StopReasonToolUse)
	}
}

func TestGenerateNon2xxReturnsError(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"model not found"}`)
	})
	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "model not found") {
		t.Errorf("error = %v, want status + body included", err)
	}
}

func TestGenerateForwardsAssistantToolCallsAndToolResults(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"message":{"content":"done"},"done":true,"done_reason":"stop"}`)
	})

	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "weather?"},
			{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{
				{ID: "call-0", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
			}},
			{Role: gantry.RoleTool, Name: "get_weather", Content: "72F"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3", len(msgs))
	}
	asst, _ := msgs[1].(map[string]any)
	tcs, _ := asst["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("assistant tool_calls len = %d, want 1", len(tcs))
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_name"] != "get_weather" {
		t.Errorf("tool message = %v, want role=tool tool_name=get_weather", toolMsg)
	}
}

func TestGeneratePropagatesContextCancellation(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"message":{"content":"x"},"done":true}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.Generate(ctx, gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
