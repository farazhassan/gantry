package skill_test

import (
	"context"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/components/skill"
	"github.com/farazhassan/gantry/harness"
)

func TestStaticSkillAlwaysMatches(t *testing.T) {
	s := skill.NewStatic("helper", "You are helpful.")
	if !s.Matches(context.Background(), &harness.State{}) {
		t.Errorf("StaticSkill should always match")
	}
	if s.Name() != "helper" || !strings.Contains(s.SystemPrompt(), "helpful") {
		t.Errorf("unexpected fields: name=%q prompt=%q", s.Name(), s.SystemPrompt())
	}
}

func TestSkillInterfaceSatisfaction(t *testing.T) {
	var _ skill.Skill = skill.NewStatic("x", "y")
}
