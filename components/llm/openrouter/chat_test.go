package openrouter_test

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
	"github.com/farazhassan/gantry/components/llm/openrouter"
)

// newServerClient spins up an httptest server with the given handler and returns
// a Client pointed at it. The server is closed via t.Cleanup.
func newServerClient(t *testing.T, handler http.HandlerFunc) *openrouter.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return openrouter.New("test-model",
		openrouter.WithAPIKey("test-key"),
		openrouter.WithBaseURL(srv.URL),
		openrouter.WithHTTPClient(srv.Client()),
	)
}

// decodeJSON reads the request body into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func TestGenerateMapsRequestAndResponse(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"hi there"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":11,"completion_tokens":7}
		}`)
	})

	resp, err := c.Generate(context.Background(), gantry.LLMRequest{
		System:   "be brief",
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer test-key")
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

func TestGenerateLengthFinishReasonMapsToMaxTokens(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"trunc"},"finish_reason":"length"}]}`)
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

func TestGenerateMapsToolCallsPreservingIDs(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{
			"choices":[{"message":{"role":"assistant","content":"",
				"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]},
				"finish_reason":"tool_calls"}]
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

	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool0["type"])
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc" {
		t.Errorf("ToolCall.ID = %q, want call_abc", tc.ID)
	}
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want get_weather", tc.Name)
	}
	if strings.TrimSpace(string(tc.Input)) != `{"city":"SF"}` {
		t.Errorf("ToolCall.Input = %s, want {\"city\":\"SF\"}", tc.Input)
	}
	if resp.StopReason != gantry.StopReasonToolUse {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, gantry.StopReasonToolUse)
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
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`)
	})

	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "weather?"},
			{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{
				{ID: "call_abc", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
			}},
			{Role: gantry.RoleTool, ToolCallID: "call_abc", Content: "72F"},
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
	tc0, _ := tcs[0].(map[string]any)
	fn, _ := tc0["function"].(map[string]any)
	if tc0["id"] != "call_abc" || fn["arguments"] != `{"city":"SF"}` {
		t.Errorf("assistant tool_call = %v, want id=call_abc arguments={\"city\":\"SF\"}", tc0)
	}
	toolMsg, _ := msgs[2].(map[string]any)
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call_abc" {
		t.Errorf("tool message = %v, want role=tool tool_call_id=call_abc", toolMsg)
	}
}

func TestGeneratePropagatesContextCancellation(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`)
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
