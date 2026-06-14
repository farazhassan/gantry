// components/ask/tool.go
package ask

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/harness"
)

// compile-time check that *Tool satisfies tool.Tool.
var _ tool.Tool = (*Tool)(nil)

const defaultName = "ask_user"

const schema = `{
  "type": "object",
  "properties": {
    "questions": {
      "type": "array",
      "minItems": 1,
      "maxItems": 4,
      "items": {
        "type": "object",
        "properties": {
          "header": {"type": "string", "maxLength": 12},
          "text": {"type": "string"},
          "options": {"type": "array", "items": {"type": "string"}, "maxItems": 4},
          "multiSelect": {"type": "boolean"}
        },
        "required": ["header", "text"]
      }
    }
  },
  "required": ["questions"]
}`

const description = "Ask the human operator one to four structured questions and return their answers. " +
	"Provide 2-4 options for a multiple-choice question; omit options for a free-text answer; " +
	"set multiSelect to allow choosing more than one option. Each question needs a short header (<=12 chars)."

// Tool is the ask_user tool. It implements tool.Tool.
type Tool struct {
	p    Prompter
	name string
}

// Option configures a Tool.
type Option func(*Tool)

// WithName overrides the default tool name ("ask_user").
func WithName(name string) Option {
	return func(t *Tool) { t.name = name }
}

// NewTool returns an ask_user tool backed by p. It panics if p is nil
// (a wiring mistake, consistent with the repo's constructor style).
func NewTool(p Prompter, opts ...Option) *Tool {
	if p == nil {
		panic("ask: NewTool requires a non-nil Prompter")
	}
	t := &Tool{p: p, name: defaultName}
	for _, o := range opts {
		o(t)
	}
	return t
}

// Definition describes the tool to the LLM.
func (t *Tool) Definition() harness.ToolDef {
	return harness.ToolDef{
		Name:        t.name,
		Description: description,
		Schema:      json.RawMessage(schema),
	}
}

// Invoke is implemented in Task 4.
func (t *Tool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	_ = ctx
	_ = input
	_ = fmt.Sprintf
	return nil, fmt.Errorf("ask: not implemented")
}
