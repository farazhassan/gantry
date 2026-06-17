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

The two curls above are **illustrative**: the second one hand-authors the whole
history with a made-up `q1` id, so it resumes regardless of what the first POST
actually returned. To prove the round trip against a real model, you have to
echo back the model's *own* tool-call id — see below.

## Manual end-to-end test (real model)

Unlike the synthetic example, a real run generates the tool-call id itself, so
you must read it off the wire and replay it. The model must support tool
calling — `llama3.2` does.

**1. Start Ollama and the server:**

```bash
ollama pull llama3.2          # tool-capable model
ollama serve                  # if not already running (default http://localhost:11434)

# in another shell:
go run ./examples/agui
# (if the linker complains: go run -ldflags=-linkmode=external ./examples/agui)
```

**2. First POST — prompt the model so it calls `ask_user`, and watch the frames:**

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","content":"Greet me by name, but ask for my name first."}]}'
```

In the SSE stream you will see (no `TOOL_CALL_RESULT` — that is the suspend
signal):

```
data: {"type":"TOOL_CALL_START","toolCallId":"call_abc123","toolCallName":"ask_user"}
data: {"type":"TOOL_CALL_ARGS","toolCallId":"call_abc123","delta":"{\"questions\":[..."}
data: {"type":"TOOL_CALL_ARGS","toolCallId":"call_abc123","delta":"...]}"}
data: {"type":"TOOL_CALL_END","toolCallId":"call_abc123"}
data: {"type":"RUN_FINISHED",...}
```

Note two things from the frames:

- the **`toolCallId`** (here `call_abc123`) — it is model-generated and differs
  every run;
- the tool **arguments**, reassembled by concatenating every `TOOL_CALL_ARGS`
  `delta` for that id in order (they stream in chunks).

**3. Second POST — resume by replaying the assistant call with the real id, then
the answer:**

Re-send the full history: the original user turn, an `assistant` message whose
`toolCalls[].id` is the captured id (and `arguments` is the reassembled string),
and a `tool` message whose `toolCallId` is the same id carrying your answer in
the `ask_user` result shape.

```bash
ID=call_abc123   # paste the id from step 2

curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d "{\"messages\":[
    {\"role\":\"user\",\"content\":\"Greet me by name, but ask for my name first.\"},
    {\"role\":\"assistant\",\"toolCalls\":[{\"id\":\"$ID\",\"type\":\"function\",\"function\":{\"name\":\"ask_user\",\"arguments\":\"{\\\"questions\\\":[{\\\"header\\\":\\\"name\\\",\\\"text\\\":\\\"Your name?\\\"}]}\"}}]},
    {\"role\":\"tool\",\"toolCallId\":\"$ID\",\"content\":\"{\\\"answers\\\":[{\\\"header\\\":\\\"name\\\",\\\"status\\\":\\\"answered\\\",\\\"values\\\":[\\\"Ada\\\"]}]}\"}
  ]}"
```

This time the stream carries `TEXT_MESSAGE_CONTENT` deltas with the model's final
greeting (e.g. "Hello, Ada!") and ends with `RUN_FINISHED`. If the
`assistant`/`tool` ids don't match, or any assistant tool call lacks a result,
the handler rejects the resume with `400`.

> Tip: the exact `arguments` string in the replayed assistant call doesn't have
> to be byte-identical to what the model emitted — what the resume path enforces
> is the id linkage (every assistant tool call has a matching `tool` result).
> Keeping it close to the captured value just keeps the transcript honest.
