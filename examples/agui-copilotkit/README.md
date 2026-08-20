# AG-UI + CopilotKit frontend actions example

Serves a single Gantry agent over the [AG-UI](https://docs.ag-ui.com) SSE
protocol, configured for **request-declared client tools** — the mechanism
CopilotKit's `useCopilotAction` relies on. Where the sibling
[`examples/agui`](../agui) advertises a tool (`ask_user`) that is fixed in Go
code via `tool.Client`, this example advertises no tools of its own at all:
every tool the model can call is declared by the *request* itself, decoded
from `RunAgentInput.tools` and installed via `tool.DynamicClient`. The server
has no idea what tools exist until a request tells it.

```bash
go run ./examples/agui-copilotkit
# (if the linker complains, use: go run -ldflags=-linkmode=external ./examples/agui-copilotkit)
```

Configurable via env: `OLLAMA_MODEL` (default `llama3.2`), `OLLAMA_HOST`,
`AGUI_ADDR` (default `:8080`), `AGUI_ALLOWED_ORIGINS` (comma-separated list,
or `*` — unset leaves CORS disabled; see below).

Like `examples/agui`, the handler itself is production-hardened by default
(server-side error logging, panic recovery, SSE keep-alives). See
[the package README](../../components/ui/agui/README.md#options) for the full
`agui.Option` list (`WithLogger`, `WithErrorMapper`, `WithHeartbeatInterval`,
`WithMaxBodyBytes`, `WithAllowedOrigins`) if you want to tune any of that
beyond what this example wires up.

### Calling it from a browser (CORS)

CORS is disabled by default, matching the package's default (the caller owns
auth/middleware). If you're calling this server directly from browser code
(e.g. a raw `fetch`, or `@ag-ui/client`'s `HttpAgent` constructed *in the
browser*) from a different origin, you'll need `AGUI_ALLOWED_ORIGINS`:

```bash
AGUI_ALLOWED_ORIGINS=http://localhost:3000 go run ./examples/agui-copilotkit
# or, for any origin during local development:
AGUI_ALLOWED_ORIGINS=* go run ./examples/agui-copilotkit
```

The `frontend/` app below does **not** need this: its browser code never
talks to this Go server directly, only to its own same-origin
`/api/copilotkit` Next.js route, which then talks to this server
server-side (not subject to browser CORS at all).

## Basic run: a request-declared tool

Unlike `examples/agui`'s `ask_user`, `get_location` below is never registered
in Go anywhere — it exists only because this one request's `tools` field
declares it, exactly how CopilotKit's `useCopilotAction` sends it:

```bash
curl -N -X POST http://localhost:8080/agui \
  -H 'Content-Type: application/json' \
  -d '{
    "messages":[{"role":"user","content":"Where am I?"}],
    "tools":[{"name":"get_location","description":"Returns the browser'\''s current location.","parameters":{"type":"object"}}]
  }'
```

If the model decides to call it, the run **suspends**: you'll see
`TOOL_CALL_START`/`TOOL_CALL_ARGS`/`TOOL_CALL_END` frames for `get_location`
and a `RUN_FINISHED`, but **no `TOOL_CALL_RESULT`** — that missing result is
the signal that the client (CopilotKit, or you) must supply an answer and
resume, the same suspend/resume contract `examples/agui` demonstrates for
`ask_user`. Because AG-UI requests are stateless, the resume POST must
**both** replay the full history (including a `tool` message with the
result) **and** re-declare the same `tools` field — the server remembers
nothing about the tool between requests. See `examples/agui`'s README for a
full manual walkthrough of that suspend/resume round trip against a real
model; the mechanics are identical here, only the tool's origin (request vs.
Go code) differs.

## A real CopilotKit frontend: `frontend/`

[`frontend/`](frontend) is a real, runnable Next.js app wired up to this
server — not a snippet. It proves the point of `tool.DynamicClient`
concretely: it registers a `get_location` frontend action via
`useCopilotAction`, backed by the browser's real `navigator.geolocation`
API — something this Go server can never do itself, since it has no access
to the browser. When the model calls `get_location`, the run suspends
exactly as in the curl example above, and CopilotKit resolves it in the
browser.

CopilotKit talks the AG-UI protocol **directly** — there is no separate
"CopilotKit wire protocol" to bridge. Connecting a custom AG-UI backend like
this one goes through a small **CopilotKit Runtime** route
(`frontend/app/api/copilotkit/route.ts`) that constructs `@ag-ui/client`'s
`HttpAgent` pointed at this Gantry server and registers it under the agent
id `gantry_demo`. The frontend's `<CopilotKit runtimeUrl="/api/copilotkit"
agent="gantry_demo">` (`frontend/app/layout.tsx`) then just names that agent
id — it never constructs or holds an `HttpAgent` itself. The only thing that
changes on the Go side, relative to a fixed-tool example like
`examples/agui`, is installing `tool.DynamicClient()` instead of
`tool.Client(...)`, since CopilotKit sends its registered frontend actions
per-request in `RunAgentInput.tools` rather than once at agent construction.

Quick start (two terminals):

```bash
# terminal 1, repo root: the Gantry AG-UI server
go run ./examples/agui-copilotkit

# terminal 2: the CopilotKit frontend
cd examples/agui-copilotkit/frontend
npm install
npm run dev
```

Then open <http://localhost:3000> and ask "Where am I?". The Runtime route
reads the Go server's URL from `GANTRY_AGUI_URL` (default
`http://localhost:8080/agui`, matching `AGUI_ADDR` above) — see
`frontend/.env.local.example`. See [`frontend/README.md`](frontend/README.md)
for more on running and structuring the app.

## Without `tool.DynamicClient`

If you remove the `agent.With(tool.DynamicClient())` call in `newHandler`,
`agui.Handler` still starts, but any request that declares `tools` — like the
curl above, or any real CopilotKit request — gets rejected with HTTP `500`
instead of silently dropping the model's call. See
[the package README](../../components/ui/agui/README.md#frontend-actions-copilotkit)
for the full explanation of why `tool.Client` alone isn't enough here.
