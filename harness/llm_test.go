package harness_test

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

// stubLLM implements harness.LLMClient for compile-time interface check.
type stubLLM struct{}

func (stubLLM) Generate(ctx context.Context, req harness.LLMRequest) (harness.LLMResponse, error) {
	return harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd}, nil
}

func TestLLMClientInterface(t *testing.T) {
	var _ harness.LLMClient = stubLLM{}
}

func TestStopReasonConstants(t *testing.T) {
	cases := []struct {
		got  harness.StopReason
		want string
	}{
		{harness.StopReasonEnd, "end_turn"},
		{harness.StopReasonToolUse, "tool_use"},
		{harness.StopReasonMaxTokens, "max_tokens"},
	}
	for _, c := range cases {
		if string(c.got) != c.want {
			t.Errorf("StopReason %q != %q", string(c.got), c.want)
		}
	}
}
