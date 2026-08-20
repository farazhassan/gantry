# agui-copilotkit frontend

A minimal Next.js (App Router) app demonstrating a real CopilotKit
connection to the Gantry AG-UI server in `../main.go`. See
[`../README.md`](../README.md) for what this example demonstrates and why;
this file only covers running this app.

## Run

Requires Node 20.9+ (Next.js 16's minimum).

```bash
# terminal 1, from the repo root: the Gantry AG-UI server
go run ./examples/agui-copilotkit

# terminal 2, from this directory
npm install
npm run dev
```

Then open <http://localhost:3000> and ask "Where am I?".

CopilotKit's Runtime prints an anonymous-telemetry notice on every build/dev
run; set `COPILOTKIT_TELEMETRY_DISABLED=true` in the environment (or in
`.env.local`) to opt out.

## Layout

- `app/api/copilotkit/route.ts` — the CopilotKit Runtime route. Constructs
  `@ag-ui/client`'s `HttpAgent` pointed at `GANTRY_AGUI_URL` (default
  `http://localhost:8080/agui`) and registers it under the agent id
  `gantry_demo`.
- `app/layout.tsx` — wraps the app in `<CopilotKit runtimeUrl="/api/copilotkit"
  agent="gantry_demo">`.
- `app/page.tsx` — a `CopilotChat` UI plus a `useCopilotAction` registration
  for `get_location`, backed by the real `navigator.geolocation` API.

## Configuration

Copy `.env.local.example` to `.env.local` to override `GANTRY_AGUI_URL` (e.g.
if the Go server isn't on the default `:8080`). `.env.local` is gitignored;
the `.example` file is the checked-in template.

No CORS configuration (`AGUI_ALLOWED_ORIGINS`) is needed for this app: the
browser only ever talks to this app's own same-origin `/api/copilotkit`
route, and the Runtime route's server-side request to the Go server isn't
subject to browser CORS at all.
