package skill_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

// TestWithSkillNoDuplicateSystemAcrossIterations guards against appending the
// skill prompt to state.System on every iteration. PhaseAssembleContext
// re-runs each iteration and state.System persists, so a per-iteration append
// would stack duplicate prompts.
func TestWithSkillNoDuplicateSystemAcrossIterations(t *testing.T) {
	mock := eval.NewMockLLMClient(
		harness.LLMResponse{ToolCalls: []harness.ToolCall{{ID: "t1", Name: "x"}}, StopReason: harness.StopReasonToolUse},
		harness.LLMResponse{Content: "final", StopReason: harness.StopReasonEnd},
	)
	a, _ := harness.NewAgent(harness.WithLLM(mock), harness.WithMaxIterations(5))
	skill.WithSkill(a, skill.NewStatic("careful", "Be careful with numbers."))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	reqs := mock.Requests()
	if len(reqs) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(reqs))
	}
	if n := strings.Count(reqs[1].System, "Be careful with numbers."); n != 1 {
		t.Errorf("second LLM call System has %d skill prompts, want 1; System=%q", n, reqs[1].System)
	}
}
