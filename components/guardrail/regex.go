package guardrail

import (
	"context"
	"fmt"
	"regexp"

	"github.com/farazhassan/gantry/harness"
)

// RegexGuardrail blocks if the configured pattern matches any inspected text.
type RegexGuardrail struct {
	pattern *regexp.Regexp
	dir     Direction
}

// NewRegex compiles pattern and returns a guardrail that runs on the
// specified direction.
func NewRegex(pattern string, dir Direction) *RegexGuardrail {
	return &RegexGuardrail{
		pattern: regexp.MustCompile(pattern),
		dir:     dir,
	}
}

func (r *RegexGuardrail) Check(_ context.Context, state *harness.State, dir Direction) error {
	if dir != r.dir {
		return nil
	}
	switch dir {
	case DirectionInput:
		for _, m := range state.Messages {
			if r.pattern.MatchString(m.Content) {
				return fmt.Errorf("%w: input matched %q", harness.ErrGuardrailBlocked, r.pattern.String())
			}
		}
	case DirectionOutput:
		if state.LastResponse != nil && r.pattern.MatchString(state.LastResponse.Content) {
			return fmt.Errorf("%w: output matched %q", harness.ErrGuardrailBlocked, r.pattern.String())
		}
	}
	return nil
}
