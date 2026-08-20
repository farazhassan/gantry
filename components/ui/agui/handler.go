package agui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/tool"
	"github.com/farazhassan/gantry/components/ui/internal/streamconfig"
)

// Handler returns an http.Handler that serves a single AG-UI run per POST. It
// decodes a RunAgentInput, reconstructs the prior conversation, and streams the
// agent's events back as AG-UI SSE frames by driving agent.RunFromStream.
//
// Scope (v1): the request's message history and client-declared tools (see
// RunAgentInput.Tools) are honored; client-supplied state is ignored. A
// request that declares tools requires components/tool.Client or
// tool.DynamicClient installed on agent — see the clientToolsReady check
// below. The caller is responsible for auth/middleware around this handler
// (CORS is the one exception — see WithAllowedOrigins). Cancellation follows
// the request context, so a client disconnect stops the run.
//
// With no opts, Handler still hardens the stream for production traffic: a
// run's terminal error is logged server-side and streamed to the client as
// RUN_ERROR; a panic anywhere in the run is recovered the same way instead of
// killing the connection with no signal; and an idle SSE keep-alive keeps the
// connection alive through reverse-proxy/load-balancer read timeouts while
// the agent is silently thinking or mid-tool-call. See the Option functions
// (WithLogger, WithErrorMapper, WithHeartbeatInterval, WithMaxBodyBytes,
// WithAllowedOrigins) to tune or disable any of this.
func Handler(agent *gantry.Agent, opts ...Option) http.Handler {
	cfg := streamconfig.Apply(opts...)

	// Computed once at construction, not per-request: whether the agent can
	// suspend on a request-declared client tool call. Without this, an
	// unresolved pending call is silently cleared by DefaultObserveHandler
	// with no result message, which the next LLM call sees as an
	// unexplained gap in the transcript — see tool.SuspendClientCallsInstalled.
	clientToolsReady := tool.SuspendClientCallsInstalled(agent)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.CORSEnabled() {
			streamconfig.ApplyCORSHeaders(w, r, cfg)
			if r.Method == http.MethodOptions {
				// The preflight itself always succeeds; an unlisted origin
				// simply gets no Access-Control-Allow-Origin above, which is
				// what makes the browser block the real request client-side.
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Cap the request body so a client cannot force unbounded allocation.
		r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		var in RunAgentInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "agui: invalid request JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(in.Messages) == 0 {
			http.Error(w, "agui: messages is empty", http.StatusBadRequest)
			return
		}
		if len(in.Tools) > 0 && !clientToolsReady {
			http.Error(w, "agui: request declares tools but the agent has no client-tool "+
				"suspend support installed (see components/tool.DynamicClient)", http.StatusInternalServerError)
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

		// run executes on its own goroutine so this goroutine is free to
		// interleave heartbeat pings on cfg.Heartbeat while it waits — a
		// silently-thinking model or a slow tool call would otherwise leave
		// the connection with no bytes on the wire, which is exactly what
		// idle-read-timeout proxies/load balancers treat as a dead
		// connection. r.Context() cancellation (client disconnect) reaches
		// run() the same way regardless of which goroutine calls it.
		resultCh := make(chan runOutcome, 1)
		go func() {
			// A panic anywhere in the run (a custom tool, an LLM adapter, the
			// mapper itself) would otherwise unwind past this goroutine and
			// crash the process — net/http's per-connection recover only
			// logs and drops the connection, leaving the client with an
			// unexplained EOF instead of a clean RUN_ERROR. Recovering here
			// and reporting it through the same channel as an ordinary error
			// keeps that one client-visible failure mode.
			defer func() {
				if p := recover(); p != nil {
					resultCh <- runOutcome{panicVal: p, stack: debug.Stack()}
				}
			}()
			_, err := run(sink.Sink())
			resultCh <- runOutcome{err: err}
		}()

		var tick <-chan time.Time
		if cfg.Heartbeat > 0 {
			ticker := time.NewTicker(cfg.Heartbeat)
			defer ticker.Stop()
			tick = ticker.C
		}

		var outcome runOutcome
	waitForRun:
		for {
			select {
			case outcome = <-resultCh:
				break waitForRun
			case <-tick:
				if err := sink.Heartbeat(); err != nil {
					// Write failed (client almost certainly gone): stop
					// pinging and just wait for run() to notice ctx
					// cancellation and return.
					tick = nil
				}
			}
		}

		switch {
		case outcome.panicVal != nil:
			cfg.Logger.Error("agui: panic during run",
				"threadId", threadID, "runId", runID,
				"panic", outcome.panicVal, "stack", string(outcome.stack))
			// The recovered value can carry arbitrary internal detail (a
			// file path, a request body, anything the panicking code had in
			// scope), so only a generic message crosses the wire — the full
			// value and stack are for the server-side log above only.
			_ = sink.EmitError(errors.New("agui: internal error"))
		case outcome.err != nil:
			cfg.Logger.Log(r.Context(), runErrorLogLevel(outcome.err), "agui: run ended with error",
				"threadId", threadID, "runId", runID, "error", outcome.err)
			_ = sink.EmitError(errors.New(cfg.SafeMapError(outcome.err, "agui: internal error")))
		}
	})
}

// runOutcome is what the goroutine running the agent reports back to the
// handler goroutine over resultCh. panicVal/stack are set instead of err
// when the run goroutine recovered a panic.
type runOutcome struct {
	err      error
	panicVal any
	stack    []byte
}

// runErrorLogLevel picks the log level for a run's terminal error. A client
// disconnect (context.Canceled, reached via r.Context()) is expected traffic
// — logging every closed tab at ERROR would be alert-fatigue noise in
// production — so it logs at Debug; anything else is a real failure and logs
// at Error.
func runErrorLogLevel(err error) slog.Level {
	if errors.Is(err, context.Canceled) {
		return slog.LevelDebug
	}
	return slog.LevelError
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
