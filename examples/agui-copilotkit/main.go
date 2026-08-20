// Command aguicopilotkit serves a single Gantry agent over the AG-UI SSE
// protocol, configured for CopilotKit-style frontend actions: instead of a
// tool declared once in Go (see examples/agui's ask_user), the tool is
// declared per-request in RunAgentInput.tools -- exactly how CopilotKit's
// useCopilotAction sends it -- and the server has no idea what tools exist
// until a request tells it.
//
// Run it, then POST a RunAgentInput whose "tools" field declares a tool the
// model can call (use curl's -N to disable buffering so tokens appear as
// produced):
//
//	go run -ldflags=-linkmode=external ./examples/agui-copilotkit
//
//	curl -N -X POST http://localhost:8080/agui \
//	  -H 'Content-Type: application/json' \
//	  -d '{
//	    "messages":[{"role":"user","content":"Where am I?"}],
//	    "tools":[{"name":"get_location","description":"Returns the browser'\''s current location.","parameters":{"type":"object"}}]
//	  }'
//
// See the README for the full suspend/resume walkthrough and for how to
// point CopilotKit's HttpAgent at this server instead of curl.
//
// The model, listen address, and agui.Handler options below are all
// configurable via env vars (see main).
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/ollama"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/ui/agui"
)

// newHandler builds the AG-UI HTTP handler for an agent that supports
// request-declared client tools (CopilotKit frontend actions): tool.DynamicClient
// installs the suspend/resume machinery, but declares no fixed tool list of its
// own -- every tool advertised to the model comes from a request's "tools"
// field, decoded by agui.Handler via tool.SetPendingClientTools. The LLM is a
// parameter so the hermetic test can inject a mock while main() wires the real
// Ollama client. opts pass straight through to agui.Handler.
func newHandler(llm gantry.LLMClient, opts ...agui.Option) (http.Handler, error) {
	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}
	if err := agent.With(tool.DynamicClient()); err != nil {
		return nil, err
	}
	return agui.Handler(agent, opts...), nil
}

func main() {
	// Defaults match the README; override for a different model, a remote
	// Ollama, or a different listen address.
	model := envOr("OLLAMA_MODEL", "llama3.2")
	addr := envOr("AGUI_ADDR", ":8080")

	ollamaOpts := []ollama.Option{}
	if base := os.Getenv("OLLAMA_HOST"); base != "" {
		ollamaOpts = append(ollamaOpts, ollama.WithBaseURL(base))
	}

	// CORS is disabled by default, matching the package's default (the caller
	// owns auth/middleware) -- but a CopilotKit frontend is *always* a
	// separate browser origin from this server, so you will need
	// AGUI_ALLOWED_ORIGINS to actually use this from CopilotKit's dev server.
	var aguiOpts []agui.Option
	if origins := parseOrigins(os.Getenv("AGUI_ALLOWED_ORIGINS")); len(origins) > 0 {
		aguiOpts = append(aguiOpts, agui.WithAllowedOrigins(origins...))
	}

	// Swap ollama.New for any gantry LLM client (openai.New, anthropic.New, …).
	handler, err := newHandler(ollama.New(model, ollamaOpts...), aguiOpts...)
	if err != nil {
		log.Fatalf("build handler: %v", err)
	}

	http.Handle("/agui", handler)
	log.Printf("AG-UI server listening on %s (POST /agui); model=%s", addr, model)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseOrigins splits AGUI_ALLOWED_ORIGINS on commas, trimming whitespace and
// dropping empty entries -- a bare comma-split would silently turn
// "http://a, http://b" into a never-matching " http://b" (leading space) or
// turn a trailing/doubled comma into an empty-string "origin".
func parseOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
