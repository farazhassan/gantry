package agui

import (
	"log/slog"
	"time"

	"github.com/farazhassan/gantry/components/ui/internal/streamconfig"
)

// Option configures optional Handler behavior — see the With* functions
// below. It is an alias for streamconfig.Option (shared with vercelai) so
// this package's public API (agui.Option, agui.WithLogger, ...) is
// unchanged by the streamconfig extraction; callers never need to know
// streamconfig exists.
type Option = streamconfig.Option

// maxRequestBytes is agui's default request-body cap (1 MiB) — see
// WithMaxBodyBytes. Kept as a package-level constant (rather than only
// living inside streamconfig) because handler_test.go references it
// directly to construct an over-the-limit request body.
const maxRequestBytes = streamconfig.DefaultMaxBodyBytes

// WithMaxBodyBytes overrides the request-body cap (default 1 MiB) applied
// via http.MaxBytesReader before decoding RunAgentInput. A replayed thread
// is text, but deployments with very long tool-heavy histories may need
// more headroom than the default, and n <= 0 is a no-op (keeps the
// default) rather than disabling the cap entirely — an unbounded body
// reader is not a safe option to expose.
func WithMaxBodyBytes(n int64) Option { return streamconfig.WithMaxBodyBytes(n) }

// WithHeartbeatInterval overrides how often Handler writes an SSE
// keep-alive comment (Sink.Heartbeat) while waiting between real Gantry
// events (default 15s). d <= 0 disables heartbeats entirely.
func WithHeartbeatInterval(d time.Duration) Option { return streamconfig.WithHeartbeatInterval(d) }

// WithLogger overrides where Handler logs a run's terminal failure
// (including a recovered panic) server-side — see WithErrorMapper for the
// separate, and intentionally different, client-visible message. The
// default is slog.Default(). A nil logger discards logs instead of
// panicking, in case a caller wires this through optional config of its
// own.
func WithLogger(l *slog.Logger) Option { return streamconfig.WithLogger(l) }

// WithErrorMapper overrides how a run's terminal error becomes the
// client-visible RUN_ERROR message. The default forwards err.Error()
// verbatim, which is fine when every configured LLM adapter already
// returns client-safe errors, but is a real information-disclosure risk
// otherwise — use this to redact or rewrite before it crosses the wire. It
// does not apply to a recovered panic, which always uses a fixed generic
// message regardless. A nil f is a no-op (keeps the default identity
// mapping).
func WithErrorMapper(f func(error) string) Option { return streamconfig.WithErrorMapper(f) }

// WithAllowedOrigins enables CORS for Handler: it answers OPTIONS
// preflight requests directly and sets Access-Control-Allow-Origin on both
// the preflight and the actual POST/SSE response for a matching Origin.
// CORS is disabled by default — the package's stated policy is that the
// caller owns auth/middleware, but AG-UI's primary clients (CopilotKit,
// the AG-UI dojo, or any browser SPA) are almost always cross-origin from
// the agent backend, so leaving every integrator to hand-roll this is a
// needless first blocker. Pass "*" for any origin; otherwise pass the
// exact origins to allow (scheme + host [+ port], no path) — every call
// replaces the previous allow-list rather than adding to it.
func WithAllowedOrigins(origins ...string) Option { return streamconfig.WithAllowedOrigins(origins...) }
