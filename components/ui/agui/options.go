package agui

import (
	"io"
	"log/slog"
	"time"
)

// config holds Handler's optional settings, assembled from Option values.
// Zero-value fields (before defaultConfig fills them in) mean "use the
// package default" — every Option is therefore purely additive and the
// zero-arg Handler(agent) call keeps its original behavior.
type config struct {
	maxBodyBytes    int64
	heartbeat       time.Duration
	logger          *slog.Logger
	mapError        func(error) string
	allowAllOrigins bool
	allowOrigins    map[string]bool
}

// defaultConfig returns the settings Handler uses when no Option overrides
// them.
func defaultConfig() *config {
	return &config{
		maxBodyBytes: maxRequestBytes,
		heartbeat:    defaultHeartbeatInterval,
		logger:       slog.Default(),
		mapError:     func(err error) string { return err.Error() },
	}
}

// noopLogger discards everything; WithLogger(nil) falls back to it rather
// than leaving cfg.logger nil (every call site would otherwise need a nil
// check).
var noopLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// defaultHeartbeatInterval is safely under common reverse-proxy/load-balancer
// idle-read timeouts (60s is a typical default for nginx/ALB), so a slow tool
// call or a silently-thinking model doesn't leave the connection looking dead
// long enough to get killed.
const defaultHeartbeatInterval = 15 * time.Second

// Option configures optional Handler behavior. Options are applied in order,
// so a later Option overrides an earlier one that touches the same setting.
type Option func(*config)

// WithMaxBodyBytes overrides the request-body cap (default 1 MiB, see
// maxRequestBytes) applied via http.MaxBytesReader before decoding
// RunAgentInput. A replayed thread is text, but deployments with very long
// tool-heavy histories may need more headroom than the default, and n <= 0
// is a no-op (keeps the default) rather than disabling the cap entirely —
// an unbounded body reader is not a safe option to expose.
func WithMaxBodyBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxBodyBytes = n
		}
	}
}

// WithHeartbeatInterval overrides how often Handler writes an SSE keep-alive
// comment (Sink.Heartbeat) while waiting between real Gantry events — see
// defaultHeartbeatInterval for why 15s is the default. d <= 0 disables
// heartbeats entirely.
func WithHeartbeatInterval(d time.Duration) Option {
	return func(c *config) {
		c.heartbeat = d
	}
}

// WithLogger overrides where Handler logs a run's terminal failure
// (including a recovered panic) server-side — see WithErrorMapper for the
// separate, and intentionally different, client-visible message. The
// default is slog.Default(), so failures are visible out of the box with no
// configuration; without this, a run failing after SSE headers are sent has
// no server-side trace at all beyond a client that may already be gone. A
// nil logger discards logs instead of panicking, in case a caller wires this
// through optional config of their own.
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l == nil {
			l = noopLogger
		}
		c.logger = l
	}
}

// WithErrorMapper overrides how a run's terminal error becomes the
// client-visible RUN_ERROR message. The default forwards err.Error()
// verbatim, which is fine when every configured LLM adapter already returns
// client-safe errors, but is a real information-disclosure risk otherwise
// (upstream response bodies, internal URLs, ...) — use this to redact or
// rewrite before it crosses the wire. It does not apply to a recovered
// panic, which always uses a fixed generic message regardless (the
// recovered value can carry arbitrary in-scope state that was never meant
// to be an error message). A nil f is a no-op (keeps the default identity
// mapping).
func WithErrorMapper(f func(error) string) Option {
	return func(c *config) {
		if f != nil {
			c.mapError = f
		}
	}
}

// WithAllowedOrigins enables CORS for Handler: it answers OPTIONS preflight
// requests directly and sets Access-Control-Allow-Origin on both the
// preflight and the actual POST/SSE response for a matching Origin. CORS is
// disabled by default (OPTIONS 405s, no Access-Control-* headers) — the
// package's stated policy is that the caller owns auth/middleware, but AG-UI's
// primary clients (CopilotKit, the AG-UI dojo, or any browser SPA) are almost
// always cross-origin from the agent backend, so leaving every integrator to
// hand-roll this is a needless first blocker. Pass "*" for any origin;
// otherwise pass the exact origins to allow (scheme + host [+ port], no
// path) — every call replaces the previous allow-list rather than adding to
// it. Calling this at all (even with no arguments) enables the preflight
// handling with an empty allow-list, matching every other origin-matching
// failure mode (no Access-Control-Allow-Origin header, so the browser blocks
// the request).
func WithAllowedOrigins(origins ...string) Option {
	return func(c *config) {
		c.allowAllOrigins = false
		c.allowOrigins = make(map[string]bool, len(origins))
		for _, o := range origins {
			if o == "*" {
				c.allowAllOrigins = true
				continue
			}
			c.allowOrigins[o] = true
		}
	}
}

// corsEnabled reports whether WithAllowedOrigins was used.
func (c *config) corsEnabled() bool {
	return c.allowAllOrigins || c.allowOrigins != nil
}

// originAllowed reports whether origin may receive
// Access-Control-Allow-Origin under this config. An empty origin (no Origin
// header, e.g. a same-origin or non-browser request) is never "allowed" —
// there's nothing to echo back and CORS headers are meaningless without it.
func (c *config) originAllowed(origin string) bool {
	return origin != "" && (c.allowAllOrigins || c.allowOrigins[origin])
}
