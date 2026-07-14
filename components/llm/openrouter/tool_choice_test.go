package openrouter_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
)

const toolChoiceReply = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`

// generateWithToolChoice runs one Generate carrying the given tool choice and
// returns the marshalled tool_choice value from the captured request body
// (the parameter is polymorphic: a bare string or an object), plus whether
// the key was present at all.
func generateWithToolChoice(t *testing.T, tc *gantry.ToolChoice) (any, bool) {
	t.Helper()
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, toolChoiceReply)
	})
	_, err := c.Generate(context.Background(), gantry.LLMRequest{
		Messages:   []gantry.Message{{Role: gantry.RoleUser, Content: "x"}},
		Tools:      []gantry.ToolDef{{Name: "get_weather", Description: "w", Schema: json.RawMessage(`{"type":"object"}`)}},
		ToolChoice: tc,
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	raw, ok := gotBody["tool_choice"]
	return raw, ok
}

func TestGenerateToolChoiceOmittedWhenNil(t *testing.T) {
	if raw, ok := generateWithToolChoice(t, nil); ok {
		t.Errorf("tool_choice = %v, want omitted for nil ToolChoice", raw)
	}
}

func TestGenerateToolChoiceStringModes(t *testing.T) {
	for _, mode := range []gantry.ToolChoiceMode{
		gantry.ToolChoiceAuto,
		gantry.ToolChoiceNone,
		gantry.ToolChoiceRequired,
	} {
		t.Run(string(mode), func(t *testing.T) {
			raw, ok := generateWithToolChoice(t, &gantry.ToolChoice{Mode: mode})
			if !ok {
				t.Fatalf("tool_choice missing for mode %q", mode)
			}
			if s, _ := raw.(string); s != string(mode) {
				t.Errorf("tool_choice = %v, want bare string %q", raw, string(mode))
			}
		})
	}
}

func TestGenerateToolChoiceForcedFunction(t *testing.T) {
	raw, ok := generateWithToolChoice(t, &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: "get_weather"})
	if !ok {
		t.Fatal("tool_choice missing for forced tool")
	}
	m, _ := raw.(map[string]any)
	if m["type"] != "function" {
		t.Errorf("tool_choice.type = %v, want function", m["type"])
	}
	fn, _ := m["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("tool_choice.function.name = %v, want get_weather", fn["name"])
	}
}
