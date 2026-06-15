// Command assistant is a terminal REPL desktop assistant that drives MCP
// servers (filesystem/fetch/time) through gantry's MCP client, persists
// conversations across restarts, and confirms mutating actions.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/session"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "assistant:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		model     = flag.String("model", envOr("ASSISTANT_MODEL", "llama3.1"), "ollama model name")
		ollamaURL = flag.String("ollama-url", os.Getenv("OLLAMA_URL"), "ollama base URL (empty = ollama default)")
		sessionID = flag.String("session", "default", "session id (conversation to resume)")
		stateDir  = flag.String("state-dir", defaultStateDir(), "directory for persisted sessions")
		fsRoot    = flag.String("fs-root", mustCwd(), "directory the filesystem server may access")
	)
	flag.Parse()

	// Cancel in-flight turns on Ctrl-C; a second Ctrl-C terminates the process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Persistence.
	store, err := checkpointer.NewFile(*stateDir)
	if err != nil {
		return fmt.Errorf("init state dir: %w", err)
	}

	// MCP servers (degraded mode: warns and continues if any fail).
	servers := connectServers(ctx, defaultServerConfigs(*fsRoot), os.Stderr)
	defer func() { _ = servers.Close() }()

	// Tools = MCP tools + ask_user.
	askTool := ask.NewTool(ask.NewCLI(os.Stdin, os.Stdout))
	allTools := append([]tool.Tool{}, servers.tools...)
	allTools = append(allTools, askTool)

	// Agent.
	agent, err := buildAgent(buildConfig{
		LLM:       newOllamaLLM(*model, *ollamaURL),
		Tools:     allTools,
		Confirmer: newCLIConfirmer(os.Stdin, os.Stdout),
	})
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}

	mgr := session.NewManager(agent, store)
	return runREPL(ctx, mgr, *sessionID, os.Stdin, os.Stdout)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".assistant-sessions"
	}
	return filepath.Join(home, ".config", "gantry-assistant", "sessions")
}

func mustCwd() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}
