// Command vercelai serves a single Gantry agent over the Vercel AI SDK v5
// UI Message Stream SSE protocol.
//
// Run it, then POST a ChatRequest and watch the UI Message Stream chunks
// stream back (use curl's -N to disable buffering so tokens appear as
// produced):
//
//	go run -ldflags=-linkmode=external ./examples/vercelai
//
//	curl -N -X POST http://localhost:8080/vercelai \
//	  -H 'Content-Type: application/json' \
//	  -d '{"messages":[{"role":"user","parts":[{"type":"text","text":"Say hello in three words."}]}]}'
//
// The model, listen address, and vercelai.Handler options below are all
// configurable via env vars (see main).
package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/ask"
	"github.com/farazhassan/gantry/components/llm/ollama"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/ui/vercelai"
)

// newHandler builds the Vercel AI SDK HTTP handler for an agent backed by
// llm. ask_user is declared as a client-side tool: when the model asks a
// question, the run suspends over the UI Message Stream (a tool part with
// no output), the client collects the answer and re-POSTs the history with
// the tool part resolved, and the run resumes. The LLM is a parameter so
// the hermetic test can inject a mock while main() wires the real Ollama
// client. opts pass straight through to vercelai.Handler.
func newHandler(llm gantry.LLMClient, opts ...vercelai.Option) (http.Handler, error) {
	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}
	if err := agent.With(tool.Client(ask.Definition())); err != nil {
		return nil, err
	}
	return vercelai.Handler(agent, opts...), nil
}

func main() {
	// Defaults match the README; override for a different model, a remote
	// Ollama, or a different listen address.
	model := envOr("OLLAMA_MODEL", "llama3.2")
	addr := envOr("VERCELAI_ADDR", ":8080")

	ollamaOpts := []ollama.Option{}
	if base := os.Getenv("OLLAMA_HOST"); base != "" {
		ollamaOpts = append(ollamaOpts, ollama.WithBaseURL(base))
	}

	// vercelai.Handler is production-hardened by default (server-side
	// error logging, panic recovery, SSE keep-alives -- see the package
	// README for WithLogger/WithErrorMapper/WithHeartbeatInterval/
	// WithMaxBodyBytes). CORS is the one thing left disabled by default:
	// set VERCELAI_ALLOWED_ORIGINS to try this server from a browser-based
	// useChat() frontend running on a different origin.
	var vercelaiOpts []vercelai.Option
	if origins := parseOrigins(os.Getenv("VERCELAI_ALLOWED_ORIGINS")); len(origins) > 0 {
		vercelaiOpts = append(vercelaiOpts, vercelai.WithAllowedOrigins(origins...))
	}

	// Swap ollama.New for any gantry LLM client (openai.New, anthropic.New, …).
	handler, err := newHandler(ollama.New(model, ollamaOpts...), vercelaiOpts...)
	if err != nil {
		log.Fatalf("build handler: %v", err)
	}

	http.Handle("/vercelai", handler)
	log.Printf("Vercel AI SDK server listening on %s (POST /vercelai); model=%s", addr, model)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseOrigins splits VERCELAI_ALLOWED_ORIGINS on commas, trimming
// whitespace and dropping empty entries -- a bare comma-split would
// silently turn "http://a, http://b" into a never-matching " http://b"
// (leading space) or turn a trailing/doubled comma into an empty-string
// "origin".
func parseOrigins(s string) []string {
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}
