package anthropic_test

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
	"github.com/farazhassan/gantry/components/llm/anthropic"
)

// newServerClient spins up an httptest server with the given handler and returns
// a Client pointed at it. The server is closed via t.Cleanup.
func newServerClient(t *testing.T, handler http.HandlerFunc) *anthropic.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return anthropic.New("test-model",
		anthropic.WithAPIKey("test-key"),
		anthropic.WithBaseURL(srv.URL),
		anthropic.WithHTTPClient(srv.Client()),
	)
}

// decodeJSON reads the request body into v.
func decodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func TestGenerateMapsRequestAndResponse(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{
			"content":[{"type":"text","text":"hi there"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":11,"output_tokens":7}
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
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", gotPath)
	}
	if gotKey != "test-key" {
		t.Errorf("x-api-key = %q, want test-key", gotKey)
	}
	if gotVersion == "" {
		t.Error("anthropic-version header missing")
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("model = %v, want test-model", gotBody["model"])
	}
	if gotBody["system"] != "be brief" {
		t.Errorf("system = %v, want top-level 'be brief'", gotBody["system"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	// max_tokens must be present and positive (Anthropic requires it).
	if mt, _ := gotBody["max_tokens"].(float64); mt <= 0 {
		t.Errorf("max_tokens = %v, want positive default", gotBody["max_tokens"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1 (user only; system is top-level)", len(msgs))
	}
	first, _ := msgs[0].(map[string]any)
	blocks, _ := first["content"].([]any)
	b0, _ := blocks[0].(map[string]any)
	if first["role"] != "user" || b0["type"] != "text" || b0["text"] != "hello" {
		t.Errorf("first message = %v, want user text 'hello'", first)
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

func TestGenerateRespectsExplicitMaxTokens(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
	})
	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages:  []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
		MaxTokens: 256,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if mt, _ := gotBody["max_tokens"].(float64); mt != 256 {
		t.Errorf("max_tokens = %v, want 256", gotBody["max_tokens"])
	}
}

func TestGenerateMaxTokensStopReason(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"trunc"}],"stop_reason":"max_tokens"}`)
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

func TestGenerateMapsToolUseBlocks(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{
			"content":[{"type":"tool_use","id":"toolu_1","name":"get_weather","input":{"city":"SF"}}],
			"stop_reason":"tool_use"
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

	// Tools forwarded with input_schema.
	tools, _ := gotBody["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(tools))
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["name"] != "get_weather" || tool0["input_schema"] == nil {
		t.Errorf("tool = %v, want name=get_weather with input_schema", tool0)
	}

	// Response tool calls: ID preserved, name + raw input mapped.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls len = %d, want 1", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "toolu_1" {
		t.Errorf("ToolCall.ID = %q, want toolu_1", tc.ID)
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
		_, _ = io.WriteString(w, `{"error":"overloaded"}`)
	})
	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
	})
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "overloaded") {
		t.Errorf("error = %v, want status + body included", err)
	}
}

func TestGenerateForwardsToolCallsAndMergesToolResults(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`)
	})

	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages: []gantry.Message{
			{Role: gantry.RoleUser, Content: "weather?"},
			{Role: gantry.RoleAssistant, ToolCalls: []gantry.ToolCall{
				{ID: "toolu_1", Name: "get_weather", Input: json.RawMessage(`{"city":"SF"}`)},
				{ID: "toolu_2", Name: "get_time", Input: json.RawMessage(`{"tz":"PT"}`)},
			}},
			{Role: gantry.RoleTool, ToolCallID: "toolu_1", Content: "72F"},
			{Role: gantry.RoleTool, ToolCallID: "toolu_2", Content: "10am"},
		},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	msgs, _ := gotBody["messages"].([]any)
	// user, assistant(tool_use x2), and a single merged user(tool_result x2).
	if len(msgs) != 3 {
		t.Fatalf("messages len = %d, want 3 (user, assistant, merged tool_results)", len(msgs))
	}
	asst, _ := msgs[1].(map[string]any)
	asstBlocks, _ := asst["content"].([]any)
	if asst["role"] != "assistant" || len(asstBlocks) != 2 {
		t.Errorf("assistant message = %v, want role=assistant with 2 tool_use blocks", asst)
	}
	tb0, _ := asstBlocks[0].(map[string]any)
	if tb0["type"] != "tool_use" || tb0["id"] != "toolu_1" {
		t.Errorf("assistant block 0 = %v, want tool_use toolu_1", tb0)
	}
	merged, _ := msgs[2].(map[string]any)
	mergedBlocks, _ := merged["content"].([]any)
	if merged["role"] != "user" || len(mergedBlocks) != 2 {
		t.Fatalf("merged tool results = %v, want role=user with 2 tool_result blocks", merged)
	}
	res0, _ := mergedBlocks[0].(map[string]any)
	if res0["type"] != "tool_result" || res0["tool_use_id"] != "toolu_1" || res0["content"] != "72F" {
		t.Errorf("tool_result 0 = %v, want tool_use_id=toolu_1 content=72F", res0)
	}
}

func TestGeneratePropagatesContextCancellation(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"x"}],"stop_reason":"end_turn"}`)
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
