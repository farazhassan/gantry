// components/ask/tool.go
package ask

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
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
          "header": {"type": "string", "minLength": 1, "maxLength": 12},
          "text": {"type": "string", "minLength": 1},
          "options": {"type": "array", "items": {"type": "string"}, "minItems": 2, "maxItems": 4},
          "multiSelect": {"type": "boolean"}
        },
        "required": ["header", "text"],
        "allOf": [
          {
            "if": {"properties": {"multiSelect": {"const": true}}},
            "then": {"required": ["options"]}
          }
        ]
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
	if t.name == "" {
		panic("ask: tool name must be non-empty")
	}
	return t
}

// Definition describes the tool to the LLM.
func (t *Tool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        t.name,
		Description: description,
		Schema:      json.RawMessage(schema),
	}
}

// Invoke parses the questions, validates them, prompts the human, and returns
// the answers as JSON for the model's next turn. A validation failure returns
// an error, which the tool middleware surfaces to the LLM as an error result
// (the run continues); the Prompter is not called in that case.
func (t *Tool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var req Request
	if err := json.Unmarshal(input, &req); err != nil {
		return nil, fmt.Errorf("ask: invalid input: %w", err)
	}
	if err := validate(req); err != nil {
		return nil, err
	}
	resp, err := t.p.Prompt(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ask: prompt: %w", err)
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("ask: marshal response: %w", err)
	}
	return out, nil
}

func validate(req Request) error {
	n := len(req.Questions)
	if n < 1 || n > 4 {
		return fmt.Errorf("ask: questions must have 1-4 items, got %d", n)
	}
	for i, q := range req.Questions {
		qi := i + 1
		switch {
		case q.Header == "":
			return fmt.Errorf("ask: question %d: header is required", qi)
		case len([]rune(q.Header)) > 12:
			return fmt.Errorf("ask: question %d: header must be <= 12 chars", qi)
		case q.Text == "":
			return fmt.Errorf("ask: question %d: text is required", qi)
		case len(q.Options) > 0 && (len(q.Options) < 2 || len(q.Options) > 4):
			return fmt.Errorf("ask: question %d: options must have 2-4 items when present, got %d", qi, len(q.Options))
		case q.MultiSelect && len(q.Options) == 0:
			return fmt.Errorf("ask: question %d: multiSelect requires options", qi)
		}
	}
	return nil
}
