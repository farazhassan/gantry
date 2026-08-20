// Package streamconfig holds the generic request/response tuning knobs
// shared by every streaming-protocol HTTP wrapper in components/ui (agui,
// vercelai, and any future protocol): max body size, SSE heartbeat
// interval, server-side logging, client-visible error rewriting, and CORS.
// None of this is protocol-specific.
package streamconfig

import (
	"io"
	"log/slog"
	"runtime/debug"
	"time"
)

// Config holds a wrapper's optional settings, assembled from Option values.
// Zero-value fields (before Default fills them in) mean "use the package
// default" — every Option is therefore purely additive, and Apply() with no
// options keeps default behavior.
type Config struct {
	MaxBodyBytes    int64
	Heartbeat       time.Duration
	Logger          *slog.Logger
	MapError        func(error) string
	AllowAllOrigins bool
	AllowOrigins    map[string]bool
}

// DefaultMaxBodyBytes is the request-body cap applied via
// http.MaxBytesReader before decoding a request (1 MiB) unless overridden
// by WithMaxBodyBytes. A replayed conversation is text, but this is
// generous while preventing unbounded allocation.
const DefaultMaxBodyBytes = 1 << 20

// DefaultHeartbeatInterval is safely under common reverse-proxy/load-balancer
// idle-read timeouts (60s is a typical nginx/ALB default), so a slow tool
// call or a silently-thinking model doesn't leave a streaming connection
// looking dead long enough to get killed.
const DefaultHeartbeatInterval = 15 * time.Second

// noopLogger discards everything; WithLogger(nil) falls back to it rather
// than leaving Config.Logger nil (every call site would otherwise need a
// nil check).
var noopLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// Default returns the settings a wrapper uses when no Option overrides them.
func Default() *Config {
	return &Config{
		MaxBodyBytes: DefaultMaxBodyBytes,
		Heartbeat:    DefaultHeartbeatInterval,
		Logger:       slog.Default(),
		MapError:     func(err error) string { return err.Error() },
	}
}

// Option configures optional wrapper behavior. Options are applied in
// order, so a later Option overrides an earlier one that touches the same
// setting.
type Option func(*Config)

// Apply returns a Default Config with every opt applied in order. A nil
// entry in opts is skipped, so a caller can build a []Option slice with
// conditional entries without filtering nils itself.
func Apply(opts ...Option) *Config {
	cfg := Default()
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(cfg)
	}
	return cfg
}

// WithMaxBodyBytes overrides the request-body cap (default
// DefaultMaxBodyBytes) applied via http.MaxBytesReader before decoding the
// request. n <= 0 is a no-op (keeps the default) rather than disabling the
// cap entirely — an unbounded body reader is not a safe option to expose.
func WithMaxBodyBytes(n int64) Option {
	return func(c *Config) {
		if n > 0 {
			c.MaxBodyBytes = n
		}
	}
}

// WithHeartbeatInterval overrides how often a bare SSE keep-alive comment
// is written while waiting between real events (default
// DefaultHeartbeatInterval). d <= 0 disables heartbeats entirely.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(c *Config) {
		c.Heartbeat = d
	}
}

// WithLogger overrides where a run's terminal failure (including a
// recovered panic) is logged server-side. The default is slog.Default(). A
// nil logger discards logs instead of panicking, in case a caller wires
// this through optional config of its own.
func WithLogger(l *slog.Logger) Option {
	return func(c *Config) {
		if l == nil {
			l = noopLogger
		}
		c.Logger = l
	}
}

// WithErrorMapper overrides how a run's terminal error becomes the
// client-visible message. The default forwards err.Error() verbatim, which
// is fine when every configured LLM adapter already returns client-safe
// errors, but is a real information-disclosure risk otherwise — use this to
// redact or rewrite before it crosses the wire. Never applies to a
// recovered panic, which always uses a fixed generic message regardless. A
// nil f is a no-op (keeps the default identity mapping).
func WithErrorMapper(f func(error) string) Option {
	return func(c *Config) {
		if f != nil {
			c.MapError = f
		}
	}
}

// WithAllowedOrigins enables CORS: it answers OPTIONS preflight requests
// directly and sets Access-Control-Allow-Origin on both the preflight and
// the actual response for a matching Origin. CORS is disabled by default —
// a wrapper's stated policy is that the caller owns auth/middleware. Pass
// "*" for any origin; otherwise pass the exact origins to allow (scheme +
// host [+ port], no path) — every call replaces the previous allow-list.
func WithAllowedOrigins(origins ...string) Option {
	return func(c *Config) {
		c.AllowAllOrigins = false
		c.AllowOrigins = make(map[string]bool, len(origins))
		for _, o := range origins {
			if o == "*" {
				c.AllowAllOrigins = true
				continue
			}
			c.AllowOrigins[o] = true
		}
	}
}

// CORSEnabled reports whether WithAllowedOrigins was used.
func (c *Config) CORSEnabled() bool {
	return c.AllowAllOrigins || c.AllowOrigins != nil
}

// OriginAllowed reports whether origin may receive
// Access-Control-Allow-Origin under this Config. An empty origin (no Origin
// header) is never "allowed" — there's nothing to echo back.
func (c *Config) OriginAllowed(origin string) bool {
	return origin != "" && (c.AllowAllOrigins || c.AllowOrigins[origin])
}

// SafeMapError applies c.MapError, recovering if it panics. A caller-
// supplied mapper runs directly in the HTTP handler goroutine, not the
// separate run goroutine a wrapper's own panic recovery covers — so without
// this, a bug in a WithErrorMapper would take the request down with an
// unexplained EOF instead of a clean client-visible error.
//
// fallback is returned verbatim whenever MapError can't produce a message
// (nil, or it panics) — callers pass their own protocol-prefixed generic
// message (e.g. "agui: internal error", "vercelai: internal error") so a
// panicking mapper still yields the same errorText convention as every
// other internal-failure path in that wrapper's handler, instead of a
// bare, unprefixed "internal error".
//
// Config's fields are all exported, so a Config can be built as a bare
// struct literal without going through Default()/Apply(), leaving Logger
// and/or MapError nil. Both are guarded here: a nil Logger falls back to
// noopLogger instead of nil-dereferencing inside the recover handler
// (defeating the whole point of this function), and a nil MapError is
// treated as an immediate failure rather than a guaranteed panic.
func (c *Config) SafeMapError(err error, fallback string) (msg string) {
	logger := c.Logger
	if logger == nil {
		logger = noopLogger
	}
	defer func() {
		if p := recover(); p != nil {
			logger.Error("streamconfig: panic in error mapper", "panic", p, "stack", string(debug.Stack()))
			msg = fallback
		}
	}()
	if c.MapError == nil {
		return fallback
	}
	return c.MapError(err)
}
