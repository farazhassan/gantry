package ollama_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
)

const toolChoiceReply = `{"message":{"role":"assistant","content":"ok"},"done":true,"done_reason":"stop"}`

func toolChoiceRequest(tc *gantry.ToolChoice) gantry.LLMRequest {
	return gantry.LLMRequest{
		Messages:   []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
		Tools:      []gantry.ToolDef{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: tc,
	}
}

func TestGenerateToolChoiceRequiredReturnsError(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the server; want rejected client-side")
	})
	_, err := c.Generate(context.Background(), toolChoiceRequest(&gantry.ToolChoice{Mode: gantry.ToolChoiceRequired}))
	if err == nil {
		t.Fatal("Generate with tool_choice required = nil error, want unsupported error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want mention of unsupported tool_choice", err)
	}
}

func TestGenerateToolChoiceForcedToolReturnsError(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the server; want rejected client-side")
	})
	_, err := c.Generate(context.Background(), toolChoiceRequest(&gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: "get_weather"}))
	if err == nil {
		t.Fatal("Generate with forced tool = nil error, want unsupported error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want mention of unsupported tool_choice", err)
	}
}

func TestGenerateStreamToolChoiceForcedToolReturnsError(t *testing.T) {
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("request reached the server; want rejected client-side")
	})
	_, err := c.GenerateStream(context.Background(),
		toolChoiceRequest(&gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: "get_weather"}),
		func(gantry.StreamChunk) error { return nil })
	if err == nil {
		t.Fatal("GenerateStream with forced tool = nil error, want unsupported error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("error = %v, want mention of unsupported tool_choice", err)
	}
}

func TestGenerateToolChoiceNoneDropsTools(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, toolChoiceReply)
	})
	_, err := c.Generate(context.Background(), toolChoiceRequest(&gantry.ToolChoice{Mode: gantry.ToolChoiceNone}))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if tools, ok := gotBody["tools"]; ok {
		t.Errorf("tools = %v, want omitted (mode none degrades by sending no tools)", tools)
	}
	if tc, ok := gotBody["tool_choice"]; ok {
		t.Errorf("tool_choice = %v, want omitted (Ollama has no such parameter)", tc)
	}
}

func TestGenerateToolChoiceAutoIsNoOp(t *testing.T) {
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, toolChoiceReply)
	})
	_, err := c.Generate(context.Background(), toolChoiceRequest(&gantry.ToolChoice{Mode: gantry.ToolChoiceAuto}))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, ok := gotBody["tools"]; !ok {
		t.Error("tools missing; mode auto must keep tools")
	}
	if tc, ok := gotBody["tool_choice"]; ok {
		t.Errorf("tool_choice = %v, want omitted (Ollama has no such parameter)", tc)
	}
}
