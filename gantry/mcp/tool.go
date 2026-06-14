package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/harness"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// compile-time check that *mcpTool satisfies tool.Tool.
var _ tool.Tool = (*mcpTool)(nil)

// config holds adapter options shared by Connect and Tools.
type config struct {
	namespace string
}

// Option configures tool wrapping.
type Option func(*config)

// WithNamespace prefixes each wrapped tool's name shown to the LLM as
// "<prefix>__<tool>", avoiding collisions when mounting several servers. The
// un-prefixed name is still used on the wire when calling the server.
func WithNamespace(prefix string) Option {
	return func(c *config) { c.namespace = prefix }
}

// mcpTool adapts a single MCP server tool to gantry's tool.Tool interface.
type mcpTool struct {
	session *mcp.ClientSession
	rawName string // name sent over the wire
	defName string // namespaced name shown to the LLM
	desc    string
	schema  json.RawMessage // MCP InputSchema, marshaled
}

// Definition implements tool.Tool.
func (t *mcpTool) Definition() harness.ToolDef {
	return harness.ToolDef{
		Name:        t.defName,
		Description: t.desc,
		Schema:      t.schema,
	}
}

// Invoke implements tool.Tool: unmarshal the LLM's arguments, call the MCP
// tool, and map the result. Transport errors and IsError results both surface
// as Go errors (recorded as ToolResult{IsError: true}; the run continues).
func (t *mcpTool) Invoke(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return nil, fmt.Errorf("mcp: %s: invalid arguments: %w", t.defName, err)
		}
	}
	res, err := t.session.CallTool(ctx, &mcp.CallToolParams{Name: t.rawName, Arguments: args})
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: call: %w", t.defName, err)
	}
	return mapResult(t.defName, res)
}

// newTool wraps one discovered MCP tool, applying namespacing from cfg.
func newTool(session *mcp.ClientSession, mt *mcp.Tool, cfg *config) (*mcpTool, error) {
	schema, err := json.Marshal(mt.InputSchema)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: marshal schema: %w", mt.Name, err)
	}
	defName := mt.Name
	if cfg.namespace != "" {
		defName = cfg.namespace + "__" + mt.Name
	}
	return &mcpTool{
		session: session,
		rawName: mt.Name,
		defName: defName,
		desc:    mt.Description,
		schema:  schema,
	}, nil
}
