package mcp

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoIn struct {
	Msg string `json:"msg" jsonschema:"the message to echo"`
}

// newTestSession starts an in-process MCP server exposing three tools and
// returns a connected client session. The session is closed via t.Cleanup.
func newTestSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-server", Version: "v0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "echoes the message"},
		func(_ context.Context, _ *mcp.CallToolRequest, in echoIn) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Msg}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "snapshot", Description: "returns an image"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte{1, 2, 3}}}}, nil, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "always errors"},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "intentional failure"}}}, nil, nil
		})

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}
