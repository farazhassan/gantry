package vercelai

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
	"github.com/farazhassan/gantry/components/ui/internal/streamconfig"
)

// Handler returns an http.Handler that serves a single Vercel AI SDK chat
// turn per POST. It decodes a ChatRequest, reconstructs the prior
// conversation, and streams the agent's events back as UI Message Stream
// SSE chunks by driving agent.RunFromStream or agent.ResumeStream.
//
// Scope (v1): the request's message history is honored; nested sub-agent
// events are not translated (see doc.go). The caller is responsible for
// auth/middleware around this handler (CORS is the one exception -- see
// WithAllowedOrigins). Cancellation follows the request context, so a
// client disconnect stops the run.
//
// With no opts, Handler still hardens the stream for production traffic:
// a run's terminal error is logged server-side and streamed to the client
// as an "error" chunk; a panic anywhere in the run is recovered the same
// way instead of killing the connection with no signal; and an idle SSE
// keep-alive keeps the connection alive through reverse-proxy/load-
// balancer read timeouts while the agent is silently thinking or
// mid-tool-call. See the Option functions (WithLogger, WithErrorMapper,
// WithHeartbeatInterval, WithMaxBodyBytes, WithAllowedOrigins) to tune or
// disable any of this.
func Handler(agent *gantry.Agent, opts ...Option) http.Handler {
	cfg := streamconfig.Apply(opts...)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.CORSEnabled() {
			streamconfig.ApplyCORSHeaders(w, r, cfg)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxBodyBytes)
		var in ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, "vercelai: invalid request JSON: "+err.Error(), http.StatusBadRequest)
			return
		}
		if len(in.Messages) == 0 {
			http.Error(w, "vercelai: messages is empty", http.StatusBadRequest)
			return
		}

		// Decide run vs resume from the terminal message before writing any
		// SSE so input errors stay clean 400s. See doc.go's "Run vs.
		// resume" note: unlike agui, there is no separate "tool" role here
		// -- an assistant-terminated history with a tool call is the
		// resume signal.
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
		case "assistant":
			prior, err := in.ToResume()
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			run = func(sink gantry.EventSink) (*gantry.State, error) {
				return agent.ResumeStream(r.Context(), prior, sink)
			}
		default:
			http.Error(w, fmt.Sprintf("vercelai: last message role = %q, want \"user\" or \"assistant\"", last.Role), http.StatusBadRequest)
			return
		}

		// Past this point the SSE response has begun; failures become
		// "error" chunks rather than HTTP status codes. Headers verified
		// against the SDK's own UI_MESSAGE_STREAM_HEADERS source;
		// Connection: keep-alive is intentionally omitted (hop-by-hop, a
		// no-op on HTTP/1.1 and disallowed on HTTP/2 -- agui made the same
		// call for the same reason).
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("x-vercel-ai-ui-message-stream", "v1")
		w.WriteHeader(http.StatusOK)

		sink := NewSink(w, newID())
		if f, ok := w.(http.Flusher); ok {
			sink.SetFlusher(f.Flush)
		}
		// Close writes the terminal "[DONE]" line the AI SDK client needs
		// to detect stream end, regardless of how the run below finished.
		defer sink.Close()

		// run executes on its own goroutine so this goroutine is free to
		// interleave heartbeat pings while it waits -- see agui's Handler
		// for the full rationale (idle-read-timeout proxies/load
		// balancers treat a silent connection as dead).
		resultCh := make(chan runOutcome, 1)
		go func() {
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
					tick = nil
				}
			}
		}

		switch {
		case outcome.panicVal != nil:
			cfg.Logger.Error("vercelai: panic during run",
				"panic", outcome.panicVal, "stack", string(outcome.stack))
			// The recovered value can carry arbitrary internal detail, so
			// only a generic message crosses the wire -- the full value
			// and stack are for the server-side log above only.
			_ = sink.EmitError(errors.New("vercelai: internal error"))
		case outcome.err != nil:
			cfg.Logger.Log(r.Context(), runErrorLogLevel(outcome.err), "vercelai: run ended with error",
				"error", outcome.err)
			_ = sink.EmitError(errors.New(cfg.SafeMapError(outcome.err)))
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

// runErrorLogLevel picks the log level for a run's terminal error. A
// client disconnect (context.Canceled, reached via r.Context()) is
// expected traffic -- logging every closed tab at ERROR would be
// alert-fatigue noise in production -- so it logs at Debug; anything else
// is a real failure and logs at Error.
func runErrorLogLevel(err error) slog.Level {
	if errors.Is(err, context.Canceled) {
		return slog.LevelDebug
	}
	return slog.LevelError
}

// newID returns a random 16-byte hex id, used as the "start" chunk's
// messageId. crypto/rand keeps it collision-safe with no third-party
// dependency.
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
