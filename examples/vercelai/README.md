# Vercel AI SDK server example

Serves a single Gantry agent over the [Vercel AI SDK](https://ai-sdk.dev) v5
UI Message Stream SSE protocol. One POST per turn; chunks stream back as
`data: {...}\n\n` frames, terminated by `data: [DONE]`.

```bash
go run ./examples/vercelai
# (if the linker complains, use: go run -ldflags=-linkmode=external ./examples/vercelai)
```

Configurable via env: `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_HOST`,
`VERCELAI_ADDR` (default `:8080`), `VERCELAI_ALLOWED_ORIGINS`
(comma-separated list, or `*` — unset leaves CORS disabled; see below).

The handler itself is production-hardened by default: a run's terminal error
is logged server-side as well as streamed to the client, a panic is
recovered into a clean `"error"` chunk instead of killing the connection,
and idle periods get an SSE keep-alive so a proxy/load-balancer read timeout
doesn't sever the stream. See
[the package README](../../components/ui/vercelai/README.md#options) for the
full `vercelai.Option` list (`WithLogger`, `WithErrorMapper`,
`WithHeartbeatInterval`, `WithMaxBodyBytes`, `WithAllowedOrigins`) if you
want to tune any of that beyond what this example wires up.

### Calling it from a browser (CORS)

CORS is disabled by default, matching the package's default (the caller owns
auth/middleware). To try this server from a browser-based `useChat()`
frontend running on a different origin, set `VERCELAI_ALLOWED_ORIGINS`
before starting it:

```bash
VERCELAI_ALLOWED_ORIGINS=http://localhost:3000 go run ./examples/vercelai
# or, for any origin during local development:
VERCELAI_ALLOWED_ORIGINS=* go run ./examples/vercelai
```

## Basic run

```bash
curl -N -X POST http://localhost:8080/vercelai \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","parts":[{"type":"text","text":"Say hello in three words."}]}]}'
```

## Human-in-the-loop: `ask_user` over the UI Message Stream

`ask_user` is declared as a **client-side tool**: it is advertised to the
model but has no server implementation. When the model calls it, the run
**suspends** — you get its `tool-input-start`/`tool-input-available` chunks
and `finish`, but **no `tool-output-available`**. That missing output is the
signal to collect an answer.

**1. First POST — the model asks a question, the run suspends:**

```bash
curl -N -X POST http://localhost:8080/vercelai \
  -H 'Content-Type: application/json' \
  -d '{"messages":[{"role":"user","parts":[{"type":"text","text":"Greet me by name."}]}]}'
```

Watch for a `tool-input-start` naming `ask_user`, its `tool-input-available`
frame, then `finish` with no `tool-output-available`.

**2. Second POST — resume by re-sending the history with that tool part
resolved:**

```bash
curl -N -X POST http://localhost:8080/vercelai \
  -H 'Content-Type: application/json' \
  -d '{"messages":[
    {"role":"user","parts":[{"type":"text","text":"Greet me by name."}]},
    {"role":"assistant","parts":[{
      "type":"dynamic-tool","toolCallId":"q1","toolName":"ask_user",
      "state":"output-available",
      "input":{"questions":[{"header":"name","text":"Your name?"}]},
      "output":{"answers":[{"header":"name","status":"answered","values":["Ada"]}]}
    }]}
  ]}'
```

A last message that is role `"assistant"` with a resolved tool part routes to
`ResumeStream`: the agent continues the transcript and streams the model's
final reply. The `toolCallId` (`q1` above) must match across turns, and
every tool part in the last message must carry a result (`state:
"output-available"`, `"output-error"`, or `"output-denied"`) or the handler
returns `400`.

The curl above is **illustrative**: it hand-authors the whole history with a
made-up `q1` id, so it resumes regardless of what the first POST actually
returned. To prove the round trip against a real model, echo back the
model's *own* tool-call id — read it off the `tool-input-start`/
`tool-input-available` chunks from step 1, the same way
[the agui example](../agui/README.md#manual-end-to-end-test-real-model)
does for AG-UI's `toolCallId`.
