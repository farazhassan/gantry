# Testing the Assistant

Two ways to verify the assistant: the automated test suite (hermetic, no
external services) and manual turn-by-turn scenarios against a live Ollama and
the real MCP servers.

> This nested `examples/assistant` module declares `go 1.25`; the core
> framework module at the repo root targets `go 1.22`. If your system Go is
> older than what a command needs, prefix it with `GOTOOLCHAIN=go1.25.x` to
> fetch the matching toolchain. Examples below include it; drop it if your Go
> is already 1.25+.

---

## 1. Automated tests (CLI)

This is a nested module, so the repo-root `go test ./...` does **not** descend
into it — run it separately. Tests are fully hermetic: an in-process stub MCP
server plus a mock LLM. No Ollama, npx, uvx, or network required.

```bash
cd examples/assistant
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go vet ./...
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go test ./...
```

The core framework module (which holds `FileCheckpointer` and the
`gantry.State` JSON round-trip test) is tested from the repo root:

```bash
cd ../..                       # repo root
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go test ./...
```

Useful variants (run from `examples/assistant`):

```bash
# verbose
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go test -v ./...

# a single test — e.g. the stdin-sharing regression test
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go test -run TestRunREPL_SharedReaderFeedsConfirmer -v

# the end-to-end agent + MCP stub tests (approve path and deny path)
GOTOOLCHAIN=go1.25.11 CGO_ENABLED=0 go test -run TestBuildAgent -v

# race detector (needs cgo, so omit CGO_ENABLED=0)
GOTOOLCHAIN=go1.25.11 go test -race ./...
```

---

## 2. Manual scenarios (live REPL)

Needs Ollama running with a tool-capable model. See the
[README](./README.md#prerequisites) for installing Ollama, `uv`, and pulling a
model.

### Set up a sandbox

Use a **real path** for `--fs-root`. Avoid `/tmp` and `$TMPDIR` on macOS: they
are symlinks under `/private`, and the filesystem server validates paths
against their resolved real path, which makes path matching confusing.

```bash
mkdir -p ~/assistant-sandbox
printf 'Gantry is a zero-dependency Go agent framework.\nProject 2 is the personal assistant example.\n' \
  > ~/assistant-sandbox/notes.txt
```

### Launch

```bash
cd examples/assistant
GOTOOLCHAIN=go1.25.11 go run . --model qwen2.5:3b --session manual --fs-root ~/assistant-sandbox
```

You should see:

```
assistant ready (session "manual"). Type /help for commands.
>
```

Type each turn at the `>` prompt. What to watch for is noted after each.

### A — Built-in commands (no LLM)

```
/help
```

Lists `/help`, `/reset`, `/exit`. Instant, no model call.

### B — Read-only tool auto-allows (no confirmation prompt)

```
List your allowed directories, then list the files there, then read notes.txt (use the full path you discovered) and summarize it in one sentence.
```

The model calls `list_allowed_directories`, `list_directory`, and
`read_text_file` — **none prompt you**. You get a one-sentence summary. This
proves the read-only allowlist works.

> Small models sometimes pass a bare `notes.txt` and the server rejects it as
> "outside allowed directories". Asking it to call `list_allowed_directories`
> first and use the full path steers it correctly.

### C — Mutating tool requires confirmation, then APPROVE

```
First call list_allowed_directories. Then create a file named hello.txt inside that exact allowed directory with the exact contents: hi from gantry
```

You'll see:

```
Confirm action: fs__write_file
  args: {
    "path": "/Users/<you>/assistant-sandbox/hello.txt",
    "content": "hi from gantry"
  }
Allow? [y/N]:
```

Type `y` and press Enter. The write executes. Verify in another terminal:

```bash
cat ~/assistant-sandbox/hello.txt   # -> hi from gantry
```

### D — Mutating tool, DENY

```
Overwrite hello.txt with the word GONE.
```

At `Allow? [y/N]:`, type `n` (or just press Enter — the default is No). You'll
see `(action denied — turn aborted, nothing was changed)`. Confirm the file is
untouched:

```bash
cat ~/assistant-sandbox/hello.txt   # still: hi from gantry
```

### E — Fetch and time servers (read-only, auto-allow)

```
What time is it in UTC right now?
```

Uses the `time__` server (via uvx), no prompt.

```
Fetch https://example.com and tell me the page title.
```

Uses the `web__fetch` server (via uvx), no prompt. Needs network.

### F — Memory within a session (should REMEMBER)

Run these two turns **without** any command in between:

```
Remember my codename is Falcon. Just acknowledge.
What is my codename?
```

The second turn must answer "Falcon". Each turn loads the prior conversation
from the session store and feeds the full history to the model, so it
remembers. If you want to confirm it is really persisted, look at the stored
messages:

```bash
cat ~/.config/gantry-assistant/sessions/*.json | python3 -m json.tool | grep -A1 -i content
```

### G — `/reset` isolates history (should FORGET)

This is the opposite test. `/reset` starts a brand-new conversation, so the
model is *expected* to forget what came before:

```
Remember my codename is Falcon. Just acknowledge.
/reset
What is my codename?
```

After `/reset` the session id changes (it announces `started a new
session ...`) and the model should **no longer** know "Falcon". Forgetting
here is correct — it proves `/reset` isolates history. (If you were testing
memory and used `/reset`, this is why it "forgot".)

### H — Ctrl-C cancels an in-flight turn (not the app)

Start a long turn, then press **Ctrl-C** while it is thinking:

```
Write a 500-word essay about Go concurrency.
```

`(turn cancelled)` prints and you return to the `>` prompt — the REPL stays
alive. A second Ctrl-C at the prompt (or `/exit`) quits.

### I — Persistence across restarts (headline feature)

State it, then quit:

```
Remember this fact: my favorite color is teal.
/exit
```

Relaunch with the **same `--session`** (and default state dir):

```bash
GOTOOLCHAIN=go1.25.11 go run . --model qwen2.5:3b --session manual --fs-root ~/assistant-sandbox
```

```
What is my favorite color?
```

It answers "teal" — loaded from disk. The state file lives at:

```bash
ls ~/.config/gantry-assistant/sessions/   # one <sha256>.json per session id
```

### J — Degraded mode (a down server does not kill the app)

If a server fails to launch (e.g. `uvx` is missing or misconfigured), startup
prints a warning and continues with the servers that did connect:

```
warning: MCP server "web" unavailable: ...
warning: MCP server "time" unavailable: ...
```

The filesystem server still works. The assistant runs degraded, not dead.

---

## Cleanup

```bash
rm -rf ~/assistant-sandbox ~/.config/gantry-assistant/sessions
```

## A note on model choice

`qwen2.5:3b` is small and fast but occasionally fumbles multi-step tool chains
or hallucinates a path before finding the right one (you'll just get extra
confirmation prompts — the safety model working as intended). For smoother
manual testing use `llama3.1` or a larger qwen; the framework behavior is
identical, the model is just more reliable.
