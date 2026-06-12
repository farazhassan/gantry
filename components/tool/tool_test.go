package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/harness"
)

type echoTool struct{}

func (echoTool) Definition() harness.ToolDef {
	return harness.ToolDef{
		Name:        "echo",
		Description: "echoes its input",
		Schema:      json.RawMessage(`{"type":"object"}`),
	}
}

func (echoTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	return input, nil
}

func TestToolInterfaceSatisfaction(t *testing.T) {
	var _ tool.Tool = echoTool{}
}
