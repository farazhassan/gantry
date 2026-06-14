// components/ask/tool_test.go
package ask_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/ask"
)

func TestDefinitionDefaultNameAndValidSchema(t *testing.T) {
	tl := ask.NewTool(ask.NewAuto(ask.Response{}))
	def := tl.Definition()
	if def.Name != "ask_user" {
		t.Errorf("Name = %q, want ask_user", def.Name)
	}
	if def.Description == "" {
		t.Errorf("Description is empty")
	}
	var v any
	if err := json.Unmarshal(def.Schema, &v); err != nil {
		t.Errorf("Schema is not valid JSON: %v", err)
	}
}

func TestWithNameOverridesToolName(t *testing.T) {
	tl := ask.NewTool(ask.NewAuto(ask.Response{}), ask.WithName("clarify"))
	if got := tl.Definition().Name; got != "clarify" {
		t.Errorf("Name = %q, want clarify", got)
	}
}

func TestNewToolPanicsOnNilPrompter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("expected panic on nil Prompter")
		}
	}()
	_ = ask.NewTool(nil)
}

// compile-time assertion lives with the implementation; this keeps ctx import used.
var _ = context.Background
