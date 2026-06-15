# Gantry Personal Assistant Example

A terminal REPL assistant built on the gantry framework. It drives three MCP
servers (filesystem, fetch, time) through `gantry/mcp`, persists each
conversation to disk, confirms mutating actions, and runs on a local Ollama
model.

## Prerequisites

- **Go 1.25+**
- **Node.js + npx** — launches the filesystem server
  (`@modelcontextprotocol/server-filesystem`).
- **uv (uvx)** — launches the official Python fetch and time reference servers
  (`mcp-server-fetch`, `mcp-server-time`). See
  <https://docs.astral.sh/uv/> to install.
- **Ollama** running locally with a tool-capable model pulled, e.g.:

  ```bash
  ollama pull llama3.1
  ```

## Run

```bash
cd examples/assistant
go run . --model llama3.1 --fs-root "$HOME/assistant-sandbox"
```

Flags (all optional):

- `--model` (env `ASSISTANT_MODEL`, default `llama3.1`) — Ollama model.
- `--ollama-url` (env `OLLAMA_URL`) — Ollama endpoint; empty uses the default.
- `--session` (default `default`) — conversation id to resume.
- `--state-dir` (default `~/.config/gantry-assistant/sessions`) — where
  conversations are persisted.
- `--fs-root` (default current directory) — directory the filesystem server
  may read/write.

## Commands

- `/help` — list commands.
- `/reset` — start a fresh conversation.
- `/exit` — quit.
- `Ctrl-C` — cancel the in-flight turn; again to quit.

## How it works

The MCP servers' tools are namespaced `fs__`, `web__`, `time__`. Read-only
tools (e.g. `fs__read_file`, `web__fetch`, all `time__`) run automatically;
anything that mutates (e.g. `fs__write_file`) prompts for `y/N` confirmation
first. Unknown tools always prompt (fail-safe). Conversation state is saved
after every turn, so restarting with the same `--session` resumes where you
left off.

The live servers are launched only when you run the binary; the test suite
uses an in-process stub and a mock LLM (no network, npx, uvx, or Ollama
needed).
