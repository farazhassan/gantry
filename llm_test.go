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
