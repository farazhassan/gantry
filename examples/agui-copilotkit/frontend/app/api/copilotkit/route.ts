// CopilotKit Runtime route: the only piece of this app that actually
// constructs @ag-ui/client's HttpAgent. It points at the Gantry AG-UI server
// (see ../../../../main.go) and registers it under the id "gantry_demo" --
// the same id the <CopilotKit agent="gantry_demo"> provider in app/layout.tsx
// selects. The frontend never constructs or holds an HttpAgent itself; it
// only ever talks to this same-origin route.
import {
  CopilotRuntime,
  copilotRuntimeNextJSAppRouterEndpoint,
  ExperimentalEmptyAdapter,
} from "@copilotkit/runtime";
import { HttpAgent } from "@ag-ui/client";
import { NextRequest } from "next/server";

// CopilotRuntime's serviceAdapter normally picks the LLM for CopilotKit's own
// built-in chat completion path. It's unused here because HttpAgent is a
// full AG-UI agent (the Gantry server) that generates its own responses, so
// an empty adapter is the standard placeholder for this shape.
const serviceAdapter = new ExperimentalEmptyAdapter();

// GANTRY_AGUI_URL points at the Go server's /agui endpoint (see
// examples/agui-copilotkit/main.go, AGUI_ADDR default :8080). Because this
// request happens server-side (this route runs on the Next.js server, not in
// the browser), the Go server's CORS settings (AGUI_ALLOWED_ORIGINS) are
// irrelevant here -- see the parent README for why.
const gantryAguiUrl =
  process.env.GANTRY_AGUI_URL ?? "http://localhost:8080/agui";

const runtime = new CopilotRuntime({
  agents: {
    // The key ("gantry_demo") is the id the frontend selects via `agent=`.
    gantry_demo: new HttpAgent({ url: gantryAguiUrl }),
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
