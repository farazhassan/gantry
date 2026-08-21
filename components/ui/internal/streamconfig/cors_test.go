package streamconfig

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplyCORSHeadersDisabled(t *testing.T) {
	cfg := Default()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	ApplyCORSHeaders(w, r, cfg)
	if h := w.Header().Get("Access-Control-Allow-Origin"); h != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want unset when CORS disabled", h)
	}
}

func TestApplyCORSHeadersExactOrigin(t *testing.T) {
	cfg := Apply(WithAllowedOrigins("https://example.com"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	ApplyCORSHeaders(w, r, cfg)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want https://example.com", got)
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Errorf("Vary = %q, want Origin for an explicit allow-list", w.Header().Get("Vary"))
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods unset")
	}
}

func TestApplyCORSHeadersWildcardSkipsVary(t *testing.T) {
	cfg := Apply(WithAllowedOrigins("*"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://anything.example")
	ApplyCORSHeaders(w, r, cfg)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := w.Header().Get("Vary"); got != "" {
		t.Errorf("Vary = %q, want unset for wildcard", got)
	}
}

func TestApplyCORSHeadersUnlistedOrigin(t *testing.T) {
	cfg := Apply(WithAllowedOrigins("https://example.com"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://evil.example")
	ApplyCORSHeaders(w, r, cfg)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want unset for an unlisted origin", got)
	}
}

func TestApplyCORSHeadersEchoesRequestedHeaders(t *testing.T) {
	cfg := Apply(WithAllowedOrigins("*"))
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodOptions, "/", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Headers", "Content-Type, X-Custom")
	ApplyCORSHeaders(w, r, cfg)
	if got := w.Header().Get("Access-Control-Allow-Headers"); got != "Content-Type, X-Custom" {
		t.Errorf("Access-Control-Allow-Headers = %q, want echoed request headers", got)
	}
}
