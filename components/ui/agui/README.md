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
- v1 honors `messages` only. Client-supplied `state` and `tools` are accepted in
  the body but ignored — Gantry tools are server-registered.

### Error behavior

- **Before streaming starts** (bad JSON, empty `messages`, non-user last
  message, unknown role) → a plain HTTP `400`/`405`, no SSE.
- **Mid-stream** (the agent errors after headers are sent) → a `RUN_ERROR`
  frame, since the `200` status is already committed.

## Using the mapper without HTTP

If you have your own HTTP stack, skip the handler and use the sink directly:

```go
sink := agui.NewSink(w, threadID, runID) // w is any io.Writer
agent.RunFromStream(ctx, prior, input, sink.Sink())
```
