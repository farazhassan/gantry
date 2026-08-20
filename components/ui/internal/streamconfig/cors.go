package streamconfig

import "net/http"

// ApplyCORSHeaders sets Access-Control-* response headers for r under cfg.
// Safe to call unconditionally on every request; it no-ops when CORS isn't
// enabled (cfg.CORSEnabled()) or the request carries no Origin header.
// Called for both the OPTIONS preflight and the actual response — browsers
// enforce CORS on the real response too, not just the preflight.
func ApplyCORSHeaders(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !cfg.CORSEnabled() {
		return
	}
	origin := r.Header.Get("Origin")
	if !cfg.OriginAllowed(origin) {
		return
	}
	if cfg.AllowAllOrigins {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	} else {
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
