package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/farazhassan/gantry"
)

// generateWithToolChoice runs one Generate carrying the given tool choice and
// returns the marshalled tool_choice object from the captured request body,
// plus whether the key was present at all.
func generateWithToolChoice(t *testing.T, tc *gantry.ToolChoice) (map[string]any, bool) {
	t.Helper()
	var gotBody map[string]any
	c := newServerClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = decodeJSON(r, &gotBody)
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`)
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
	if !ok {
		return nil, false
	}
	m, _ := raw.(map[string]any)
	return m, true
}

func TestGenerateToolChoiceOmittedWhenNil(t *testing.T) {
	if m, ok := generateWithToolChoice(t, nil); ok {
		t.Errorf("tool_choice = %v, want omitted for nil ToolChoice", m)
	}
}

func TestGenerateToolChoiceModes(t *testing.T) {
	cases := []struct {
		mode     gantry.ToolChoiceMode
		wantType string
	}{
		{gantry.ToolChoiceAuto, "auto"},
		{gantry.ToolChoiceNone, "none"},
		{gantry.ToolChoiceRequired, "any"}, // gantry "required" is Anthropic "any"
	}
	for _, c := range cases {
		t.Run(string(c.mode), func(t *testing.T) {
			m, ok := generateWithToolChoice(t, &gantry.ToolChoice{Mode: c.mode})
			if !ok {
				t.Fatalf("tool_choice missing for mode %q", c.mode)
			}
			if m["type"] != c.wantType {
				t.Errorf("tool_choice.type = %v, want %q", m["type"], c.wantType)
			}
			if _, hasName := m["name"]; hasName {
				t.Errorf("tool_choice.name present for mode %q; want omitted", c.mode)
			}
		})
	}
}

func TestGenerateToolChoiceForcedTool(t *testing.T) {
	m, ok := generateWithToolChoice(t, &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: "get_weather"})
	if !ok {
		t.Fatal("tool_choice missing for forced tool")
	}
	if m["type"] != "tool" || m["name"] != "get_weather" {
		t.Errorf(`tool_choice = %v, want {"type":"tool","name":"get_weather"}`, m)
	}
}
