package agui

import "net/http"

// applyCORSHeaders sets Access-Control-* response headers for r under cfg.
// Safe to call unconditionally on every request; it no-ops when CORS isn't
// enabled (cfg.corsEnabled()) or the request carries no Origin header.
// Called for both the OPTIONS preflight and the actual POST — browsers
// enforce CORS on the real response too, not just the preflight.
func applyCORSHeaders(w http.ResponseWriter, r *http.Request, cfg *config) {
	if !cfg.corsEnabled() {
		return
	}
	origin := r.Header.Get("Origin")
	if !cfg.originAllowed(origin) {
		return
	}
	if cfg.allowAllOrigins {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
		// Echoing a specific origin (rather than "*") makes the response
		// origin-dependent, so caches must key on it too.
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", http.MethodPost+", "+http.MethodOptions)
	if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", reqHeaders)
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	}
	w.Header().Set("Access-Control-Max-Age", "600")
}
