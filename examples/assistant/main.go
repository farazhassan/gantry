// Command assistant is a terminal REPL desktop assistant that drives MCP
// servers (filesystem/fetch/time) through gantry's MCP client, persists
// conversations across restarts, and confirms mutating actions.
package main

import (
	"bufio"
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

// defaultPersona is the assistant's base system prompt. It establishes the
// assistant's role and a few behavioral norms; the systemprompt middleware
// applies it on every turn (System is not carried across turns), so editing
// this text takes effect on the next turn.
const defaultPersona = "You are a helpful personal desktop assistant. " +
	"You can read and write files, fetch web pages, and tell the time through your tools. " +
	"Be concise and friendly. " +
	"Before creating, editing, moving, or deleting anything, briefly say what you're about to do. " +
	"If you're unsure which file or path the user means, ask rather than guess."

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

	// Interrupt handling is armed per turn (see runREPL / armSignalInterrupt):
	// a Ctrl-C during a turn cancels just that turn, while a Ctrl-C at the idle
	// prompt falls through to Go's default handler and terminates the process.
	ctx := context.Background()

	// Persistence.
	store, err := checkpointer.NewFile(*stateDir)
	if err != nil {
		return fmt.Errorf("init state dir: %w", err)
	}

	// MCP servers (degraded mode: warns and continues if any fail).
	servers := connectServers(ctx, defaultServerConfigs(*fsRoot), os.Stderr)
	defer func() { _ = servers.Close() }()

	// One shared buffered reader over stdin. The REPL, the confirmer, and the
	// ask prompter all read lines from the same console; giving each its own
	// bufio.Reader would let one read-ahead and swallow input the others need
	// (notably the confirmer's y/N line). bufio.NewReader returns this same
	// reader when handed it again, so every consumer shares one buffer.
	stdin := bufio.NewReader(os.Stdin)

	// Tools = MCP tools + ask_user.
	askTool := ask.NewTool(ask.NewCLI(stdin, os.Stdout))
	allTools := append([]tool.Tool{}, servers.tools...)
	allTools = append(allTools, askTool)

	// Agent.
	agent, err := buildAgent(buildConfig{
		LLM:          newOllamaLLM(*model, *ollamaURL),
		Tools:        allTools,
		Confirmer:    newCLIConfirmer(stdin, os.Stdout),
		SystemPrompt: defaultPersona,
	})
	if err != nil {
		return fmt.Errorf("build agent: %w", err)
	}

	mgr := session.NewManager(agent, store)
	return runREPL(ctx, mgr, *sessionID, stdin, os.Stdout, armSignalInterrupt)
}

// armSignalInterrupt installs an os.Interrupt handler that cancels the current
// turn, returning a disarm func that removes it. It is the production
// armInterrupt: signal.Notify is registered only while a turn runs, so an idle
// Ctrl-C at the prompt is left to Go's default behaviour (terminate). The
// disarm func stops delivery and unblocks the watcher goroutine.
func armSignalInterrupt(cancel context.CancelFunc) func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			cancel()
		case <-done:
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
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
