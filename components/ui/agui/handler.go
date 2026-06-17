package agui

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/farazhassan/gantry"
)

// Handler returns an http.Handler that serves a single AG-UI run per POST. It
// decodes a RunAgentInput, reconstructs the prior conversation, and streams the
// agent's events back as AG-UI SSE frames by driving agent.RunFromStream.
//
// Scope (v1): the request's message history is honored; client-supplied state
// and tools are ignored. The caller is responsible for auth/middleware around
// this handler. Cancellation follows the request context, so a client
// disconnect stops the run.
func Handler(agent *gantry.Agent) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Cap the request body so a client cannot force unbounded allocation.
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		var in RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "agui: invalid request JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(in.Messages) == 0 {
			http.Error(w, "agui: messages is empty", http.StatusBadRequest)
			return
		}

		// Decide run vs resume from the terminal message before writing any SSE
		// so input errors stay clean 400s. A user-terminated history starts/continues
		// a turn (RunFromStream); a tool-terminated history fulfills a suspended
		// client tool call and resumes the transcript as-is (ResumeStream).
		last := in.Messages[len(in.Messages)-1]
		var run func(sink gantry.EventSink) (*gantry.State, error)
		switch last.Role {
		case "user":
			prior, input, err := in.ToRun()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			run = func(sink gantry.EventSink) (*gantry.State, error) {
				return agent.RunFromStream(r.Context(), prior, input, sink)
			}
		case "tool":
			prior, err := in.ToResume()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			run = func(sink gantry.EventSink) (*gantry.State, error) {
				return agent.ResumeStream(r.Context(), prior, sink)
			}
		default:
			http.Error(w, fmt.Sprintf("agui: last message role = %q, want \"user\" or \"tool\"", last.Role), http.StatusBadRequest)
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
		// Connection is a hop-by-hop header: it's a no-op on HTTP/1.1 (keep-alive
		// is already the default) and disallowed on HTTP/2, so we don't set it.
		// X-Accel-Buffering disables response buffering in nginx and similar
		// reverse proxies, which otherwise defeats live SSE streaming.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		sink := NewSink(w, threadID, runID)
		if f, ok := w.(http.Flusher); ok {
			sink.SetFlusher(f.Flush)
		}

		if _, err := run(sink.Sink()); err != nil {
			_ = sink.EmitError(err)
		}
	})
}

// maxRequestBytes caps the decoded RunAgentInput body (1 MiB). A replayed
// thread is text; this is generous while preventing unbounded allocation.
const maxRequestBytes = 1 << 20

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
