package skill

import (
	"context"

	"github.com/farazhassan/gantry/harness"
)

// StaticSkill always matches and always contributes the same prompt.
type StaticSkill struct {
	name   string
	prompt string
}

// NewStatic returns a StaticSkill.
func NewStatic(name, prompt string) *StaticSkill {
	return &StaticSkill{name: name, prompt: prompt}
}

func (s *StaticSkill) Name() string                                 { return s.name }
func (s *StaticSkill) SystemPrompt() string                         { return s.prompt }
func (s *StaticSkill) Matches(context.Context, *harness.State) bool { return true }
