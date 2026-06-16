// Package mcp connects to Model Context Protocol (MCP) servers and exposes
// their tools as gantry tool.Tool values.
//
// Connect is the common path: it launches a stdio MCP server subprocess,
// performs the MCP handshake, snapshots the server's tools once, and returns a
// *Server whose Tools method yields gantry tools ready to register with an
// agent. Tools is the thin adapter for callers that already hold an SDK
// *mcp.ClientSession.
//
// This package lives in its own module so the third-party MCP SDK never enters
// the dependency graph of gantry's zero-dependency core.
package mcp
