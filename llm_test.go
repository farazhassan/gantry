package gantry_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry"
)

// stubLLM implements gantry.LLMClient for compile-time interface check.
type stubLLM struct{}

func (stubLLM) Generate(ctx context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error) {
	return gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd}, nil
}

func TestLLMClientInterface(t *testing.T) {
	var _ gantry.LLMClient = stubLLM{}
}

func TestStopReasonConstants(t *testing.T) {
	cases := []struct {
		got  gantry.StopReason
		want string
	}{
		{gantry.StopReasonEnd, "end_turn"},
		{gantry.StopReasonToolUse, "tool_use"},
		{gantry.StopReasonMaxTokens, "max_tokens"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("StopReason %q != %q", string(c.got), c.want)
		}
	}
}

func TestToolChoiceModeConstants(t *testing.T) {
	cases := []struct {
		got  gantry.ToolChoiceMode
		want string
	}{
		{gantry.ToolChoiceAuto, "auto"},
		{gantry.ToolChoiceNone, "none"},
		{gantry.ToolChoiceRequired, "required"},
		{gantry.ToolChoiceTool, "tool"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("ToolChoiceMode %q != %q", string(c.got), c.want)
		}
	}
}

func TestLLMRequestCarriesToolChoice(t *testing.T) {
	req := gantry.LLMRequest{
		ToolChoice: &gantry.ToolChoice{Mode: gantry.ToolChoiceTool, Name: "emit_plan"},
	}
	if req.ToolChoice.Mode != gantry.ToolChoiceTool || req.ToolChoice.Name != "emit_plan" {
		t.Errorf("ToolChoice = %+v, want {Mode: tool, Name: emit_plan}", req.ToolChoice)
	}
	// nil means "provider default" — the zero value must be nil.
	var zero gantry.LLMRequest
	if zero.ToolChoice != nil {
		t.Errorf("zero LLMRequest.ToolChoice = %v, want nil", zero.ToolChoice)
	}
}
