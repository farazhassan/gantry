package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/tool"
)

// ToolSuite verifies the contract of tool.Tool. Factory must return a tool
// that handles a JSON-object input (it does not need to use the contents).
func ToolSuite(t *testing.T, factory func() tool.Tool) {
	t.Helper()

	t.Run("definition_has_name", func(t *testing.T) {
		tl := factory()
		def := tl.Definition()
		if def.Name == "" {
			t.Errorf("Definition().Name is empty")
		}
		// Schema should be valid JSON.
		var v any
		if err := json.Unmarshal(def.Schema, &v); err != nil {
			t.Errorf("Schema is not valid JSON: %v", err)
		}
	})

	t.Run("invoke_returns_or_errors", func(t *testing.T) {
		tl := factory()
		out, err := tl.Invoke(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			// An error is acceptable — but it must be non-nil and have a message.
			if err.Error() == "" {
				t.Errorf("error has empty message")
			}
			return
		}
		if out == nil {
			t.Errorf("Invoke returned nil output with no error")
		}
	})
}
