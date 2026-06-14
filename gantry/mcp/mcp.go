package mcp

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/farazhassan/gantry/components/tool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ServerConfig describes a stdio MCP server to launch.
type ServerConfig struct {
	Command string   // executable, e.g. "npx"
	Args    []string // arguments, e.g. ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
	Env     []string // optional os.Environ()-style entries; nil inherits the parent environment
}

// Server is a connected MCP server with a static snapshot of its tools.
type Server struct {
	session *mcp.ClientSession
	tools   []tool.Tool
}

// Connect launches the configured stdio MCP server, performs the handshake,
// snapshots its tools once, and returns a *Server. Call Close to shut down the
// subprocess.
func Connect(ctx context.Context, cfg ServerConfig, opts ...Option) (*Server, error) {
	if cfg.Command == "" {
		return nil, fmt.Errorf("mcp: ServerConfig.Command is required")
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gantry-mcp", Version: "v0.1.0"}, nil)

	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.Env != nil {
		cmd.Env = cfg.Env
	}
	transport := &mcp.CommandTransport{Command: cmd}

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %q: %w", cfg.Command, err)
	}
	tools, err := Tools(ctx, session, opts...)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	return &Server{session: session, tools: tools}, nil
}

// Tools returns the static snapshot of tools captured at Connect.
func (s *Server) Tools() []tool.Tool { return s.tools }

// Close shuts down the MCP session and its subprocess.
func (s *Server) Close() error { return s.session.Close() }

// Tools wraps every tool advertised by an already-connected MCP session as a
// gantry tool.Tool. The caller retains ownership of session. Discovery is a
// one-time snapshot; tools added by the server later are not reflected.
func Tools(ctx context.Context, session *mcp.ClientSession, opts ...Option) ([]tool.Tool, error) {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	var tools []tool.Tool
	for mt, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("mcp: list tools: %w", err)
		}
		wrapped, err := newTool(session, mt, cfg)
		if err != nil {
			return nil, err
		}
		tools = append(tools, wrapped)
	}
	return tools, nil
}
