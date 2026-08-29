# AG-UI

This package exposes a Gantry agent over the [AG-UI](https://docs.ag-ui.com)
(Agent-User Interaction) protocol as a Server-Sent Events (SSE) stream, so any
AG-UI-compatible client can drive the agent and render a live conversation with
tool use.

It is built in three layers (see `doc.go` for the full overview):

- **Wire layer** (`events.go`) — AG-UI event DTOs + `WriteSSE` framing.
- **Mapper + Sink** (`mapper.go`, `sink.go`) — translate Gantry's
  `gantry.Event` stream into AG-UI events; usable with your own HTTP stack.
- **Handler** (`handler.go`, `input.go`) — a thin `net/http.Handler` that
  decodes a `RunAgentInput`, rebuilds the prior conversation, and drives
  `agent.RunFromStream`.

## Frontend actions (CopilotKit)

CopilotKit's `useCopilotAction` registers a tool in the browser and sends it
per-request in `RunAgentInput.tools` — there is no separate CopilotKit wire
protocol; it talks AG-UI directly via `@ag-ui/client`'s `HttpAgent`. This
package honors that field, but the agent must opt in to suspending on a
call with no server-side implementation:

```go
agent, err := gantry.NewAgent(gantry.WithLLM(llm))
if err != nil {
    log.Fatal(err)
}
if err := agent.With(tool.DynamicClient()); err != nil {
    log.Fatal(err)
}
http.Handle("/agui", agui.Handler(agent))
```

Without `tool.DynamicClient()` installed, `Handler` rejects (HTTP 500) any
request that declares `tools`, rather than silently dropping the model's
call. If your agent's tool set never varies per request, prefer the static
`tool.Client(defs...)` instead — see `components/tool`'s package doc. The
two are mutually exclusive on one agent (installing both returns an error).

## Testing it yourself

### 1. Run the unit + integration tests

The package ships exhaustive unit tests for every layer plus one `httptest`
end-to-end test that POSTs a `RunAgentInput`, reads the SSE response, and
asserts the event sequence. They use a mock LLM, so **no provider key or network
access is needed**.

This repo's tests require the external linker:

```bash
go test -ldflags=-linkmode=external ./components/ui/agui/...
go vet ./components/ui/agui/...
```

(If a sandbox blocks the external linker or the test's local TCP listener,
re-run with sandboxing disabled.)

### 2. Run a live server and stream from it

A runnable server lives at [`examples/agui`](../../../examples/agui). It wraps
`agui.Handler` around an agent backed by the local Ollama adapter (no API key);
swap in any `gantry` LLM client you have configured. Run it from the repo root:

```bash
go run -ldflags=-linkmode=external ./examples/agui
```

The `-ldflags=-linkmode=external` flag is required on macOS: the default
internal linker can emit a binary without an `LC_UUID` load command, which newer
macOS refuses to run ("missing LC_UUID load command"). The external linker fixes
it — the same reason this repo's tests use that flag.

Configure via env vars: `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_HOST`
(default `http://localhost:11434`), `AGUI_ADDR` (default `:8080`).

Then POST a `RunAgentInput` and watch the SSE frames stream back. The `-N` flag
disables curl buffering so you see tokens as they arrive:

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "threadId": "demo-thread",
    "runId": "demo-run",
    "messages": [
      { "role": "user", "content": "Say hello in three words." }
    ]
  }'
```

You should see a stream of SSE frames, each `data: {...}\n\n`, beginning with
`RUN_STARTED`, then a `TEXT_MESSAGE_START` / `TEXT_MESSAGE_CONTENT` … /
`TEXT_MESSAGE_END` group, and ending with `RUN_FINISHED`:

```
data: {"type":"RUN_STARTED","threadId":"demo-thread","runId":"demo-run"}

data: {"type":"TEXT_MESSAGE_START","messageId":"demo-run:msg:1","role":"assistant"}

data: {"type":"TEXT_MESSAGE_CONTENT","messageId":"demo-run:msg:1","delta":"Hello"}

data: {"type":"TEXT_MESSAGE_END","messageId":"demo-run:msg:1"}

data: {"type":"RUN_FINISHED","threadId":"demo-thread","runId":"demo-run"}
```

### Request notes

- `messages` is the full replayed thread. The **last** message must have
  `role: "user"` — it becomes the new turn's input; everything before it is
  reconstructed as prior conversation state.
- `threadId` / `runId` are optional; if omitted, the handler generates random
  ones.
- v1 honors `messages` and, when `tool.DynamicClient` is installed, `tools` —
  see "Frontend actions" above. Client-supplied `state` is still accepted in
  the body but ignored.

### AG-UI event coverage

Implemented: `RUN_STARTED`/`RUN_FINISHED`/`RUN_ERROR`, `STEP_STARTED`/
`STEP_FINISHED`, `TEXT_MESSAGE_START`/`TEXT_MESSAGE_CONTENT`/
`TEXT_MESSAGE_END`, `TOOL_CALL_START`/`TOOL_CALL_ARGS`/`TOOL_CALL_END`/
`TOOL_CALL_RESULT`, `SUBAGENT_STARTED`/`SUBAGENT_FINISHED`/`SUBAGENT_ERROR`
(a nested sub-agent run's lifecycle — `SUBAGENT_FINISHED` carries
`outcome:{"type":"suspended"}` when the run paused on a client-tool call
rather than actually finishing), `CUSTOM` (gantry-specific: `gantry.usage`,
`gantry.events_dropped`), `REASONING_START`/
`REASONING_MESSAGE_START`/`REASONING_MESSAGE_CONTENT`/
`REASONING_MESSAGE_END`/`REASONING_END` (translated generically from any
adapter's `StreamChunk.ReasoningDelta` — see `anthropic.WithExtendedThinking`,
`openai.WithReasoningEffort`, `ollama.WithThinking`, and
`openrouter.WithReasoningEffort`), `RAW` (provider frames this package
doesn't otherwise model), and `ACTIVITY_SNAPSHOT`/`ACTIVITY_DELTA`
(`components/planner`'s `update_plan` plan-step progress). The AG-UI spec's
optional `subagentRunId` attribution field (the RunID of the nested run that
produced an event) is stamped on every event type above except the three
top-level lifecycle events (`RUN_STARTED`/`RUN_FINISHED`/`RUN_ERROR`), which
leave it empty.

Not implemented, deliberately: `STATE_SNAPSHOT`/`STATE_DELTA`/
`MESSAGES_SNAPSHOT` (no v1 synchronizable agent state — see "Request notes"
above), the `*_CHUNK` convenience events (redundant with the explicit
Start/Content/End sequence this package always emits),
`REASONING_ENCRYPTED_VALUE` (no adapter round-trips thinking
signatures/encrypted blocks back to the provider yet), `SUBAGENT_STARTED`'s
`parentMessageId` (no gantry concept of "the message holding a tool call"),
`interruptIds` on a suspended outcome (no `Interrupt` id concept in gantry
yet), and preserving the same `subagentRunId` across a suspend/resume round
trip (gantry mints a fresh RunID on every `Agent.Resume` call today).

### Error behavior

- **Before streaming starts** (bad JSON, empty `messages`, non-user last
  message, unknown role) → a plain HTTP `400`/`405`, no SSE.
- **`tools` declared without `tool.DynamicClient` installed** → HTTP `500`
  (a server misconfiguration, not a client error — see "Frontend actions" above).
- **Mid-stream** (the agent errors after headers are sent, or panics) → a
  `RUN_ERROR` frame, since the `200` status is already committed. The error is
  also logged server-side (see `WithLogger`); a panic's recovered value/stack
  go only to that log, never to the client (see `WithErrorMapper`).

## Options

`Handler(agent, opts ...Option)` is production-hardened by default; every
`Option` tunes or opts out of one piece of that:

| Option | Default | Purpose |
| --- | --- | --- |
| `WithMaxBodyBytes(n)` | 1 MiB | Cap on the decoded `RunAgentInput` body. |
| `WithHeartbeatInterval(d)` | 15s | How often an SSE keep-alive comment (`: ping`) is sent while waiting between real events, so a slow tool call or a silently-thinking model doesn't leave the connection looking dead to a reverse proxy/load balancer's idle-read timeout. `d <= 0` disables it. |
| `WithLogger(l)` | `slog.Default()` | Where a run's terminal error (or a recovered panic, with its full value + stack) is logged server-side. |
| `WithErrorMapper(f)` | forwards `err.Error()` verbatim | Rewrites a run error into the client-visible `RUN_ERROR` message — use this to redact anything an LLM adapter's error might carry (upstream response bodies, internal URLs, ...). Never applies to a panic, which always gets a fixed generic message client-side regardless. |
| `WithAllowedOrigins(origins...)` | disabled | Enables CORS: answers `OPTIONS` preflight and sets `Access-Control-Allow-Origin` on both the preflight and the actual response for a listed origin. Pass `"*"` for any origin. Disabled by default — an unconfigured `Handler` behaves exactly as before this option existed (`OPTIONS` → `405`, no `Access-Control-*` headers). |

```go
http.Handle("/agui", agui.Handler(agent,
	agui.WithAllowedOrigins("https://my-frontend.example"),
	agui.WithErrorMapper(func(error) string { return "internal error" }),
))
```

## Using the mapper without HTTP

If you have your own HTTP stack, skip the handler and use the sink directly:

```go
sink := agui.NewSink(w, threadID, runID) // w is any io.Writer
agent.RunFromStream(ctx, prior, input, sink.Sink())
```

Handler's idle keep-alive is built on `sink.Heartbeat()`, which writes a bare
SSE comment (`: ping`) and flushes — safe to call concurrently with `Sink()`
(both are guarded by the same mutex). If you're driving `Sink` from your own
HTTP stack, call it on your own idle ticker to get the same protection.
