package mcp_test

import (
	"context"
	"fmt"

	"github.com/farazhassan/gantry/mcp"
)

func ExampleConnect() {
	ctx := context.Background()

	srv, err := mcp.Connect(ctx, mcp.ServerConfig{
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	}, mcp.WithNamespace("fs"))
	if err != nil {
		panic(err)
	}
	defer srv.Close()

	for _, t := range srv.Tools() {
		fmt.Println(t.Definition().Name)
	}
	// Register srv.Tools() with a gantry agent's tool list here.
}
