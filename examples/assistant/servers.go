package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/farazhassan/gantry/components/tool"
	gmcp "github.com/farazhassan/gantry/mcp"
)

// namespacedServer pairs an MCP server launch config with the namespace its
// tools are mounted under (fs__, web__, time__).
type namespacedServer struct {
	Namespace string
	Config    gmcp.ServerConfig
}

// defaultServerConfigs returns the three MCP servers the assistant mounts.
// fsRoot is the directory the filesystem server is allowed to access.
//
// The filesystem server is an npm package launched via npx; the fetch and time
// servers are the official Python reference servers launched via uvx (see
// README for toolchain prerequisites). Tests never launch these.
func defaultServerConfigs(fsRoot string) []namespacedServer {
	return []namespacedServer{
		{
			Namespace: "fs",
			Config: gmcp.ServerConfig{
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", fsRoot},
			},
		},
		{
			Namespace: "web",
			Config: gmcp.ServerConfig{
				Command: "uvx",
				Args:    []string{"mcp-server-fetch"},
			},
		},
		{
			Namespace: "time",
			Config: gmcp.ServerConfig{
				Command: "uvx",
				Args:    []string{"mcp-server-time"},
			},
		},
	}
}

// connectedServers holds the live MCP servers and their aggregated tools.
type connectedServers struct {
	servers []*gmcp.Server
	tools   []tool.Tool
}

// connectServers connects each configured server, aggregating their tools.
// A server that fails to connect is logged to warn and skipped (degraded
// mode) — a single down server must not kill the assistant. The caller must
// call Close when done.
func connectServers(ctx context.Context, cfgs []namespacedServer, warn io.Writer) *connectedServers {
	cs := &connectedServers{}
	for _, ns := range cfgs {
		srv, err := gmcp.Connect(ctx, ns.Config, gmcp.WithNamespace(ns.Namespace))
		if err != nil {
			fmt.Fprintf(warn, "warning: MCP server %q unavailable: %v\n", ns.Namespace, err)
			continue
		}
		cs.servers = append(cs.servers, srv)
		cs.tools = append(cs.tools, srv.Tools()...)
	}
	return cs
}

// Close shuts down every connected server, joining any errors so that one
// server's failure neither short-circuits the others nor hides their errors.
func (cs *connectedServers) Close() error {
	var errs []error
	for _, s := range cs.servers {
		if err := s.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
