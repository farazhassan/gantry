package agui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/farazhassan/gantry/harness"
)

// Handler returns an http.Handler that serves a single AG-UI run per POST. It
// decodes a RunAgentInput, reconstructs the prior conversation, and streams the
// agent's events back as AG-UI SSE frames by driving agent.RunFromStream.
//
// Scope (v1): the request's message history is honored; client-supplied state
// and tools are ignored. The caller is responsible for auth/middleware around
// this handler. Cancellation follows the request context, so a client
// disconnect stops the run.
func Handler(agent *harness.Agent) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var in RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "agui: invalid request JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		prior, input, err := in.ToRun()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		threadID := in.ThreadID
		if threadID == "" {
			threadID = newID()
		}
		runID := in.RunID
		if runID == "" {
			runID = newID()
		}

		// Past this point the SSE response has begun; failures become RUN_ERROR
		// frames rather than HTTP status codes.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		sink := NewSink(w, threadID, runID)
		if f, ok := w.(http.Flusher); ok {
			sink.SetFlusher(f.Flush)
		}

		if _, err := agent.RunFromStream(r.Context(), prior, input, sink.Sink()); err != nil {
			_ = sink.EmitError(err)
		}
	})
}

// newID returns a random 16-byte hex id, used when the client omits threadId or
// runId. crypto/rand keeps it collision-safe with no third-party dependency.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read never returns an error on supported platforms; fall back to
		// a fixed-but-valid id rather than panicking in a request handler.
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
