package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// jsonUnmarshal is a small indirection used by this package's tests so they do
// not sprinkle encoding/json references.
func jsonUnmarshal(b []byte, out any) error { return json.Unmarshal(b, out) }

// mapResult converts an MCP tool result into the JSON value gantry hands back
// to the LLM. Text content is concatenated (newline-separated); non-text blocks
// become a short placeholder; empty content maps to an empty string. A result
// flagged IsError is surfaced as a Go error (carrying the tool name and any
// text) so gantry dispatch records a ToolResult{IsError: true} and the run
// continues.
func mapResult(name string, res *mcp.CallToolResult) (json.RawMessage, error) {
	text := joinContent(res.Content)
	if res.IsError {
		if text == "" {
			return nil, fmt.Errorf("mcp: %s: tool reported an error", name)
		}
		return nil, fmt.Errorf("mcp: %s: %s", name, text)
	}
	out, err := json.Marshal(text)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: marshal result: %w", name, err)
	}
	return out, nil
}

func joinContent(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, c := range content {
		switch v := c.(type) {
		case *mcp.TextContent:
			parts = append(parts, v.Text)
		case *mcp.ImageContent:
			parts = append(parts, fmt.Sprintf("[image: %s omitted]", mimeOr(v.MIMEType, "binary")))
		case *mcp.AudioContent:
			parts = append(parts, fmt.Sprintf("[audio: %s omitted]", mimeOr(v.MIMEType, "binary")))
		default:
			parts = append(parts, fmt.Sprintf("[%T omitted]", c))
		}
	}
	return strings.Join(parts, "\n")
}

func mimeOr(mime, fallback string) string {
	if mime == "" {
		return fallback
	}
	return mime
}
