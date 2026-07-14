package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

// delegateTool wraps a child *gantry.Agent as a tool.Tool. Invoke runs the
// child inline — a fresh run seeded with the goal plus an optional context
// briefing — and returns the child's FinalOutput as the tool result.
type delegateTool struct {
	name        string
	description string
	child       *gantry.Agent
}

// Option configures a delegate tool built by New.
type Option func(*delegateTool)

// New wraps child as a tool the parent's LLM can invoke. name and description
// are advertised verbatim in the ToolDef; the input schema is fixed:
// {"goal" (required), "context" (optional briefing string)}. child must be
// non-nil; a nil child makes every Invoke return a tool error.
func New(name, description string, child *gantry.Agent, opts ...Option) tool.Tool {
	t := &delegateTool{name: name, description: description, child: child}
	for _, o := range opts {
		if o != nil {
			o(t)
		}
	}
	return t
}

// compile-time check: delegateTool implements tool.Tool.
var _ tool.Tool = (*delegateTool)(nil)

// Definition describes the delegate to the parent's LLM.
func (t *delegateTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
		Name:        t.name,
		Description: t.description,
		Schema: json.RawMessage(`{
  "type": "object",
  "properties": {
    "goal": {"type": "string", "description": "What the sub-agent should accomplish."},
    "context": {"type": "string", "description": "Optional briefing: relevant facts, constraints, or prior findings the sub-agent needs. It sees nothing else from this conversation."}
  },
  "required": ["goal"]
}`),
	}
}

// Invoke runs the child agent inline and returns {"output": <FinalOutput>}.
// The child sees ONLY the goal and optional context — a fresh run, no parent
// transcript. Child run errors return as tool errors: dispatch surfaces them
// to the parent's model as an error tool result and the parent run continues.
//
// Run (not Resume) is the child entry point: Run mints a fresh gantry.State
// via NewState (Trace and Meta initialized) and DefaultStartHandler seeds the
// combined goal+context as the opening user message. Resume is the primitive
// for continuing an interrupted prior state and would require hand-assembling
// a *State, duplicating NewState's initialization.
func (t *delegateTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in struct {
		Goal    string `json:"goal"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("%s: invalid input: %w", t.name, err)
	}
	if in.Goal == "" {
		return nil, fmt.Errorf("%s: goal is required", t.name)
	}
	if t.child == nil {
		return nil, errors.New(t.name + ": no child agent configured")
	}

	seed := in.Goal
	if in.Context != "" {
		seed = in.Goal + "\n\nContext:\n" + in.Context
	}

	st, err := t.child.Run(ctx, seed)
	if err != nil {
		return nil, fmt.Errorf("%s: child run failed: %w", t.name, err)
	}
	out, mErr := json.Marshal(map[string]string{"output": st.FinalOutput})
	if mErr != nil {
		return nil, fmt.Errorf("%s: encoding output: %w", t.name, mErr)
	}
	return out, nil
}
