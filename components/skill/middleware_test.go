package skill_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/eval"
)

func TestWithSkillInjectsPromptWhenMatches(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(skill.New(skill.NewStatic("helper", "Be concise."))); err != nil {
		t.Fatalf("install skill: %v", err)
	}

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
func (onlyOnFlag) Matches(ctx context.Context, s *gantry.State) bool {
	v, _ := s.Meta["flag"].(bool)
	return v
}

func TestWithSkillSkipsWhenNotMatches(t *testing.T) {
	mock := eval.NewMockLLMClient(gantry.LLMResponse{Content: "ok", StopReason: gantry.StopReasonEnd})
	a, _ := gantry.NewAgent(gantry.WithLLM(mock))
	if err := a.With(skill.New(onlyOnFlag{})); err != nil {
		t.Fatalf("install skill: %v", err)
	}

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(mock.Requests()[0].System, "FLAG ON") {
		t.Errorf("skill should not have matched")
	}
}
