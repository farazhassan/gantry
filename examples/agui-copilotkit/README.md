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
auth/middleware). Unlike a curl-only demo, a CopilotKit frontend is *always*
a separate browser origin from this server — CopilotKit runs inside your web
app's own dev server (e.g. `http://localhost:3000`), not alongside the Go
process — so you will need `AGUI_ALLOWED_ORIGINS` for any real integration:

```bash
AGUI_ALLOWED_ORIGINS=http://localhost:3000 go run ./examples/agui-copilotkit
# or, for any origin during local development:
AGUI_ALLOWED_ORIGINS=* go run ./examples/agui-copilotkit
```

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

## Wiring an actual CopilotKit frontend

This is the part that's new relative to `examples/agui`. CopilotKit talks the
AG-UI protocol **directly** — there is no separate "CopilotKit wire protocol"
to bridge — via `@ag-ui/client`'s `HttpAgent`. That means nothing changes on
the CopilotKit side beyond pointing `HttpAgent` at a Gantry-backed URL; the
only thing that changes on the Go side is installing `tool.DynamicClient()`
instead of `tool.Client(...)`, since CopilotKit sends its registered actions
per-request in `RunAgentInput.tools` rather than once at agent construction.

This repo ships no JS/npm tooling, so the snippet below is illustrative — it
shows the shape of a real integration, not a runnable file in this repo:

```ts
import { HttpAgent } from "@ag-ui/client";
import { CopilotKit, useCopilotAction } from "@copilotkit/react-core";

// Point CopilotKit's HttpAgent at this server (AGUI_ADDR, default :8080).
const agent = new HttpAgent({
  url: "http://localhost:8080/agui",
});

function LocationAction() {
  // Registers a *frontend* action: the tool's definition (name, description,
  // parameters) is sent to the server per-request in RunAgentInput.tools, and
  // its handler/render run entirely in the browser -- nothing is executed
  // server-side. This is exactly what tool.DynamicClient advertises and
  // suspends on: the model calls "get_location", the Go server has never
  // heard of it, the run suspends, and CopilotKit resolves it here.
  useCopilotAction({
    name: "get_location",
    description: "Returns the browser's current location.",
    parameters: [],
    handler: async () => {
      const pos = await new Promise<GeolocationPosition>((resolve, reject) =>
        navigator.geolocation.getCurrentPosition(resolve, reject),
      );
      return { lat: pos.coords.latitude, lng: pos.coords.longitude };
    },
    render: ({ status }) => <span>Locating you… ({status})</span>,
  });
  return null;
}

export default function App() {
  return (
    <CopilotKit runtimeUrl={undefined} agent={agent}>
      <LocationAction />
      {/* your chat UI */}
    </CopilotKit>
  );
}
```

`render` (or a returned result from `handler`) is what makes this a
*frontend* action rather than a server-side tool: the call is fulfilled by
code running in the user's browser (here, the Geolocation API), not by Go.

## Without `tool.DynamicClient`

If you remove the `agent.With(tool.DynamicClient())` call in `newHandler`,
`agui.Handler` still starts, but any request that declares `tools` — like the
curl above, or any real CopilotKit request — gets rejected with HTTP `500`
instead of silently dropping the model's call. See
[the package README](../../components/ui/agui/README.md#frontend-actions-copilotkit)
for the full explanation of why `tool.Client` alone isn't enough here.
