// Command agui serves a single Gantry agent over the AG-UI SSE protocol.
//
// Run it, then POST a RunAgentInput and watch the AG-UI event frames stream
// back (use curl's -N to disable buffering so tokens appear as produced):
//
//	go run -ldflags=-linkmode=external ./examples/agui
//
//	curl -N -X POST http://localhost:8080/agui \
//	  -H 'Content-Type: application/json' \
//	  -d '{"messages":[{"role":"user","content":"Say hello in three words."}]}'
//
// The model and listen address are configurable via env vars (see main).
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/ollama"
	"github.com/farazhassan/gantry/components/ui/agui"
)

// newHandler builds the AG-UI HTTP handler for an agent backed by llm. The LLM
// is a parameter so the hermetic test can inject a mock while main() wires the
// real Ollama client.
func newHandler(llm gantry.LLMClient) (http.Handler, error) {
	agent, err := gantry.NewAgent(gantry.WithLLM(llm))
	if err != nil {
		return nil, err
	}
	return agui.Handler(agent), nil
}

func main() {
	// Defaults match the README; override for a different model, a remote
	// Ollama, or a different listen address.
	model := envOr("OLLAMA_MODEL", "llama3.2")
	addr := envOr("AGUI_ADDR", ":8080")

	opts := []ollama.Option{}
	if base := os.Getenv("OLLAMA_HOST"); base != "" {
		opts = append(opts, ollama.WithBaseURL(base))
	}

	// Swap ollama.New for any harness LLM client (openai.New, anthropic.New, …).
	handler, err := newHandler(ollama.New(model, opts...))
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
