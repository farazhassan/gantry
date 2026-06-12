package skill_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/eval"
	"github.com/farazhassan/gantry/harness"
)

func TestWithSkillInjectsPromptWhenMatches(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.New(harness.WithLLM(mock))
	skill.WithSkill(a, skill.NewStatic("helper", "Be concise."))

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	req := mock.Requests()[0]
	if !strings.Contains(req.System, "Be concise.") {
		t.Errorf("system prompt missing skill content: %q", req.System)
	}
}

type onlyOnFlag struct{}

func (onlyOnFlag) Name() string         { return "flag" }
func (onlyOnFlag) SystemPrompt() string { return "FLAG ON" }
func (onlyOnFlag) Matches(ctx context.Context, s *harness.State) bool {
	v, _ := s.Meta["flag"].(bool)
	return v
}

func TestWithSkillSkipsWhenNotMatches(t *testing.T) {
	mock := eval.NewMockLLMClient(harness.LLMResponse{Content: "ok", StopReason: harness.StopReasonEnd})
	a, _ := harness.New(harness.WithLLM(mock))
	skill.WithSkill(a, onlyOnFlag{})

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(mock.Requests()[0].System, "FLAG ON") {
		t.Errorf("skill should not have matched")
	}
}
