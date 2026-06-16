package tool_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
)

type echoTool struct{}

func (echoTool) Definition() gantry.ToolDef {
	return gantry.ToolDef{
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
