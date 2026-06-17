# AG-UI server example

Serves a single Gantry agent over the [AG-UI](https://docs.ag-ui.com) SSE
protocol. One POST per run; events stream back as AG-UI frames.

```bash
go run ./examples/agui
# (if the linker complains, use: go run -ldflags=-linkmode=external ./examples/agui)
```

Configurable via env: `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_HOST`,
`AGUI_ADDR` (default `:8080`).

## Basic run

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Say hello in three words."}]}'
```

## Human-in-the-loop: `ask_user` over AG-UI

`ask_user` is declared as a **client-side tool**: it is advertised to the model
but has no server implementation. When the model calls it, the run **suspends** —
you get the tool's `TOOL_CALL_START/ARGS/END` frames and `RUN_FINISHED`, but **no
`TOOL_CALL_RESULT`**. That missing result is the signal to collect an answer.

**1. First POST — the model asks a question, the run suspends:**

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Greet me by name."}]}'
```

Watch for a `TOOL_CALL_START` naming `ask_user`, its argument frames, then
`RUN_FINISHED` with no `TOOL_CALL_RESULT`.

**2. Second POST — resume by re-sending the history ending in a `tool` result:**

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{"messages":[
    {"role":"user","content":"Greet me by name."},
    {"role":"assistant","toolCalls":[{"id":"q1","type":"function","function":{"name":"ask_user","arguments":"{\"questions\":[{\"header\":\"name\",\"text\":\"Your name?\"}]}"}}]},
    {"role":"tool","toolCallId":"q1","content":"{\"answers\":[{\"header\":\"name\",\"status\":\"answered\",\"values\":[\"Ada\"]}]}"}
  ]}'
```

A `tool`-terminated history routes to `ResumeStream`: the agent continues the
transcript and streams the model's final reply. The `id`/`toolCallId` (`q1`
above) must match, and every assistant tool call must have a corresponding tool
result or the handler returns `400`.
