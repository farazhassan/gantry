# Vercel AI SDK (UI Message Stream)

This package exposes a Gantry agent over the [Vercel AI SDK](https://ai-sdk.dev)
v5 **UI Message Stream** protocol as a Server-Sent Events (SSE) stream, so a
`useChat()`-based frontend (or any client speaking the same protocol) can
drive the agent and render a live conversation with tool use.

It is built in three layers (see `doc.go` for the full overview):

- **Wire layer** (`chunks.go`) -- UI Message Stream chunk DTOs + `WriteSSE`
  framing.
- **Mapper + Sink** (`mapper.go`, `sink.go`) -- translate Gantry's
  `gantry.Event` stream into UI Message Stream chunks; usable with your own
  HTTP stack.
- **Handler** (`handler.go`, `input.go`) -- a thin `net/http.Handler` that
  decodes a `ChatRequest`, rebuilds the prior conversation, and drives
  `agent.RunFromStream`/`agent.ResumeStream`.

## Testing it yourself

### 1. Run the unit + integration tests

The package ships unit tests for every layer plus `httptest`-based
end-to-end tests that POST a `ChatRequest`, read the SSE response, and
assert the chunk sequence. They use a mock LLM, so **no provider key or
network access is needed**.

This repo's tests require the external linker:

```bash
go test -ldflags=-linkmode=external ./components/ui/vercelai/...
go vet -ldflags=-linkmode=external ./components/ui/vercelai/...
```

### 2. Run a live server and stream from it

A runnable server lives at [`examples/vercelai`](../../../examples/vercelai).
It wraps `vercelai.Handler` around an agent backed by the local Ollama
adapter (no API key); swap in any `gantry` LLM client you have configured.
Run it from the repo root:

```bash
go run -ldflags=-linkmode=external ./examples/vercelai
```

Configure via env vars: `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_HOST`
(default `http://localhost:11434`), `VERCELAI_ADDR` (default `:8080`).

Then POST a `ChatRequest` and watch the SSE frames stream back:

```bash
curl -N -X POST http://localhost:8080/vercelai \
  -H 'Content-Type: application/json' \
  -d '{
    "id": "demo-chat",
    "messages": [
      { "role": "user", "parts": [{ "type": "text", "text": "Say hello in three words." }] }
    ]
  }'
```

You should see a stream of SSE frames, each `data: {...}\n\n`, beginning with
`start`, then a `text-start` / `text-delta` ... / `text-end` group, and ending
with `finish` and the terminal `data: [DONE]`:

```
data: {"type":"start","messageId":"..."}

data: {"type":"text-start","id":"...:text:1"}

data: {"type":"text-delta","id":"...:text:1","delta":"Hello"}

data: {"type":"text-end","id":"...:text:1"}

data: {"type":"finish"}

data: [DONE]
```

### Pointing a real `useChat()` frontend at it

```ts
const { messages, sendMessage } = useChat({
  transport: new DefaultChatTransport({ api: "http://localhost:8080/vercelai" }),
});
```

CORS is disabled by default (the caller owns auth/middleware) -- set
`VERCELAI_ALLOWED_ORIGINS` (see the example's `main.go`) or pass
`vercelai.WithAllowedOrigins(...)` directly to try this from a browser-based
frontend on a different origin.

### Request notes

- `messages` is the full replayed thread of `UIMessage`s. The **last**
  message must be either role `"user"` (a fresh turn) or role `"assistant"`
  with at least one resolved tool part (a resume after a client-side tool
  call) -- see "Human-in-the-loop" below.
- v1 honors `messages` only; `id`/`trigger` are accepted but not trusted --
  run-vs-resume is derived from the message shape itself.

### AG-UI-equivalent scope note

See `doc.go` for the full list of what's deliberately out of scope for v1
(sub-agent passthrough, reasoning round-trip across turns, multimodal
input) -- the same posture agui's own `doc.go` takes for its gaps.

### Error behavior

- **Before streaming starts** (bad JSON, empty `messages`, an invalid last
  message) -> a plain HTTP `400`/`405`, no SSE.
- **Mid-stream** (the agent errors after headers are sent, or panics) -> an
  `"error"` chunk, since the `200` status is already committed. The error is
  also logged server-side (see `WithLogger`); a panic's recovered
  value/stack go only to that log, never to the client (see
  `WithErrorMapper`). Every stream, success or failure, ends with a
  terminal `data: [DONE]` line.

## Options

`Handler(agent, opts ...Option)` is production-hardened by default; every
`Option` tunes or opts out of one piece of that -- the same five options as
`agui.Handler`, backed by the shared `components/ui/internal/streamconfig`
package:

| Option | Default | Purpose |
| --- | --- | --- |
| `WithMaxBodyBytes(n)` | 1 MiB | Cap on the decoded `ChatRequest` body. |
| `WithHeartbeatInterval(d)` | 15s | How often an SSE keep-alive comment (`: ping`) is sent while waiting between real events. `d <= 0` disables it. |
| `WithLogger(l)` | `slog.Default()` | Where a run's terminal error (or a recovered panic, with its full value + stack) is logged server-side. |
| `WithErrorMapper(f)` | forwards `err.Error()` verbatim | Rewrites a run error into the client-visible `"error"` chunk's `errorText`. Never applies to a panic. |
| `WithAllowedOrigins(origins...)` | disabled | Enables CORS: answers `OPTIONS` preflight and sets `Access-Control-Allow-Origin` on both the preflight and the actual response for a listed origin. Pass `"*"` for any origin. |

```go
http.Handle("/vercelai", vercelai.Handler(agent,
	vercelai.WithAllowedOrigins("https://my-frontend.example"),
	vercelai.WithErrorMapper(func(error) string { return "internal error" }),
))
```

## Using the mapper without HTTP

If you have your own HTTP stack, skip the handler and use the sink directly:

```go
sink := vercelai.NewSink(w, messageID) // w is any io.Writer
agent.RunFromStream(ctx, prior, input, sink.Sink())
sink.Close() // writes the terminal "data: [DONE]" line
```

Handler's idle keep-alive is built on `sink.Heartbeat()`, which writes a bare
SSE comment (`: ping`) and flushes -- safe to call concurrently with `Sink()`
(both are guarded by the same mutex). If you're driving `Sink` from your own
HTTP stack, call it on your own idle ticker to get the same protection, and
call `sink.Close()` once when the response is done regardless of outcome.
