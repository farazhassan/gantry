package main

import (
	"context"
	"testing"

	"github.com/farazhassan/gantry/components/tool"
	gmcp "github.com/farazhassan/gantry/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

type writeIn struct {
	Path    string `json:"path" jsonschema:"file path"`
	Content string `json:"content" jsonschema:"file contents"`
}

type readIn struct {
	Path string `json:"path" jsonschema:"file path"`
}

// newStubFSTools starts an in-process MCP server exposing a fake filesystem
// with one read-only tool (read_file) and one mutating tool (write_file),
// then wraps them through the real gantry/mcp adapter under the "fs"
// namespace. The session is closed via t.Cleanup. Returns the namespaced
// tools (fs__read_file, fs__write_file).
func newStubFSTools(t *testing.T) []tool.Tool {
	t.Helper()
	ctx := context.Background()

	server := sdkmcp.NewServer(&sdkmcp.Implementation{Name: "stub-fs", Version: "v0"}, nil)
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "read_file", Description: "reads a file"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in readIn) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "contents of " + in.Path}}}, nil, nil
		})
	sdkmcp.AddTool(server, &sdkmcp.Tool{Name: "write_file", Description: "writes a file"},
		func(_ context.Context, _ *sdkmcp.CallToolRequest, in writeIn) (*sdkmcp.CallToolResult, any, error) {
			return &sdkmcp.CallToolResult{Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "wrote " + in.Path}}}, nil, nil
		})

	clientT, serverT := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("stub server connect: %v", err)
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "stub-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("stub client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tools, err := gmcp.Tools(ctx, session, gmcp.WithNamespace("fs"))
	if err != nil {
		t.Fatalf("wrap stub tools: %v", err)
	}
	return tools
}
