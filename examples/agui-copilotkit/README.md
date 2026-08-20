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
to bridge. But connecting a custom AG-UI backend like this one still goes
through a small **CopilotKit Runtime** route: a backend endpoint (typically a
Next.js API route) that constructs `@ag-ui/client`'s `HttpAgent` pointed at
your Gantry server and registers it by name. The frontend's `<CopilotKit>`
component then just points `runtimeUrl` at that route and names the agent by
the id it was registered under — it does not construct or hold an `HttpAgent`
itself. The only thing that changes on the Go side is installing
`tool.DynamicClient()` instead of `tool.Client(...)`, since CopilotKit sends
its registered frontend actions per-request in `RunAgentInput.tools` rather
than once at agent construction.

This repo ships no JS/npm tooling, so the snippets below are illustrative —
they show the shape of a real integration, not runnable files in this repo.

**1. Backend Runtime route** (e.g. `app/api/copilotkit/route.ts` in a Next.js
App Router project) — this is the piece that actually constructs `HttpAgent`:

```ts
// app/api/copilotkit/route.ts
import {
  CopilotRuntime,
  copilotRuntimeNextJSAppRouterEndpoint,
  ExperimentalEmptyAdapter,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";
import { NextRequest } from "next/server";

// CopilotRuntime's serviceAdapter normally picks the LLM for CopilotKit's own
// built-in chat completion path; it's unused here because HttpAgent is a
// full AG-UI agent (this Gantry server) that generates its own responses, so
// an empty adapter is the standard placeholder for this shape. (Import names
// under @copilotkit/runtime have moved across versions -- check your
// installed version's docs if this doesn't match exactly.)
const serviceAdapter = new ExperimentalEmptyAdapter();

const runtime = new CopilotRuntime({
  agents: {
    // Points at this example server (AGUI_ADDR, default :8080). The key
    // ("gantry_demo") is the id the frontend below selects via `agent=`.
    gantry_demo: new HttpAgent({ url: "http://localhost:8080/agui" }),
  },
});

export const POST = async (req: NextRequest) => {
  const { handleRequest } = copilotRuntimeNextJSAppRouterEndpoint({
    runtime,
    serviceAdapter,
    endpoint: "/api/copilotkit",
  });
  return handleRequest(req);
};
```

**2. Frontend** — `runtimeUrl` points at the Runtime route above, and `agent`
is the **string id** it was registered under (not an `HttpAgent` instance):

```tsx
import { CopilotKit, useCopilotAction } from "@copilotkit/react-core";

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
    <CopilotKit runtimeUrl="/api/copilotkit" agent="gantry_demo">
      <LocationAction />
      {/* your chat UI */}
    </CopilotKit>
  );
}
```

`render` (or a returned result from `handler`) is what makes `get_location` a
*frontend* action rather than a server-side tool: the call is fulfilled by
code running in the user's browser (here, the Geolocation API), not by Go.

## Without `tool.DynamicClient`

If you remove the `agent.With(tool.DynamicClient())` call in `newHandler`,
`agui.Handler` still starts, but any request that declares `tools` — like the
curl above, or any real CopilotKit request — gets rejected with HTTP `500`
instead of silently dropping the model's call. See
[the package README](../../components/ui/agui/README.md#frontend-actions-copilotkit)
for the full explanation of why `tool.Client` alone isn't enough here.
