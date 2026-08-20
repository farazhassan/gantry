package vercelai

import (
	"log/slog"
	"time"

	"github.com/farazhassan/gantry/components/ui/internal/streamconfig"
)

// Option configures optional Handler behavior -- see the With* functions
// below. It is an alias for streamconfig.Option (shared with agui).
type Option = streamconfig.Option

// WithMaxBodyBytes overrides the request-body cap (default 1 MiB) applied
// via http.MaxBytesReader before decoding ChatRequest. A replayed
// conversation is text, but deployments with very long tool-heavy
// histories may need more headroom than the default, and n <= 0 is a
// no-op (keeps the default) rather than disabling the cap entirely.
func WithMaxBodyBytes(n int64) Option { return streamconfig.WithMaxBodyBytes(n) }

// WithHeartbeatInterval overrides how often Handler writes an SSE
// keep-alive comment (Sink.Heartbeat) while waiting between real Gantry
// events (default 15s). d <= 0 disables heartbeats entirely.
func WithHeartbeatInterval(d time.Duration) Option { return streamconfig.WithHeartbeatInterval(d) }

// WithLogger overrides where Handler logs a run's terminal failure
// (including a recovered panic) server-side. The default is
// slog.Default(). A nil logger discards logs instead of panicking.
func WithLogger(l *slog.Logger) Option { return streamconfig.WithLogger(l) }

// WithErrorMapper overrides how a run's terminal error becomes the
// client-visible "error" chunk's errorText. The default forwards
// err.Error() verbatim, which is fine when every configured LLM adapter
// already returns client-safe errors, but is a real information-
// disclosure risk otherwise -- use this to redact or rewrite before it
// crosses the wire. Never applies to a recovered panic, which always uses
// a fixed generic message regardless.
func WithErrorMapper(f func(error) string) Option { return streamconfig.WithErrorMapper(f) }

// WithAllowedOrigins enables CORS for Handler: it answers OPTIONS
// preflight requests directly and sets Access-Control-Allow-Origin on
// both the preflight and the actual POST/SSE response for a matching
// Origin. CORS is disabled by default -- the package's stated policy is
// that the caller owns auth/middleware. Pass "*" for any origin;
// otherwise pass the exact origins to allow (scheme + host [+ port], no
// path) -- every call replaces the previous allow-list rather than adding
// to it.
func WithAllowedOrigins(origins ...string) Option { return streamconfig.WithAllowedOrigins(origins...) }
