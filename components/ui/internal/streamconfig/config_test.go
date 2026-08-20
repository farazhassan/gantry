package streamconfig

import (
	"errors"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	c := Default()
	if c.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want %d", c.MaxBodyBytes, DefaultMaxBodyBytes)
	}
	if c.Heartbeat != DefaultHeartbeatInterval {
		t.Errorf("Heartbeat = %v, want %v", c.Heartbeat, DefaultHeartbeatInterval)
	}
	if c.Logger == nil {
		t.Error("Logger is nil, want slog.Default()")
	}
	if c.MapError == nil {
		t.Fatal("MapError is nil")
	}
	if got := c.MapError(errors.New("boom")); got != "boom" {
		t.Errorf("default MapError(boom) = %q, want %q", got, "boom")
	}
	if c.CORSEnabled() {
		t.Error("CORSEnabled() = true, want false for a fresh Default()")
	}
}

func TestApplySkipsNilOptions(t *testing.T) {
	c := Apply(nil, WithMaxBodyBytes(42), nil)
	if c.MaxBodyBytes != 42 {
		t.Errorf("MaxBodyBytes = %d, want 42", c.MaxBodyBytes)
	}
}

func TestWithMaxBodyBytesIgnoresNonPositive(t *testing.T) {
	c := Apply(WithMaxBodyBytes(0))
	if c.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want default %d (n<=0 must be a no-op)", c.MaxBodyBytes, DefaultMaxBodyBytes)
	}
	c2 := Apply(WithMaxBodyBytes(-5))
	if c2.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("MaxBodyBytes = %d, want default %d for negative n", c2.MaxBodyBytes, DefaultMaxBodyBytes)
	}
}

func TestWithHeartbeatIntervalAllowsDisabling(t *testing.T) {
	c := Apply(WithHeartbeatInterval(0))
	if c.Heartbeat != 0 {
		t.Errorf("Heartbeat = %v, want 0 (disabled)", c.Heartbeat)
	}
}

func TestWithLoggerNilFallsBackToNoop(t *testing.T) {
	c := Apply(WithLogger(nil))
	if c.Logger == nil {
		t.Fatal("Logger is nil, want the noop fallback logger")
	}
}

func TestWithErrorMapperNilIsNoop(t *testing.T) {
	c := Apply(WithErrorMapper(nil))
	if got := c.MapError(errors.New("x")); got != "x" {
		t.Errorf("MapError(x) = %q, want identity mapping preserved", got)
	}
}

func TestWithAllowedOriginsWildcard(t *testing.T) {
	c := Apply(WithAllowedOrigins("*"))
	if !c.CORSEnabled() {
		t.Fatal("CORSEnabled() = false, want true")
	}
	if !c.AllowAllOrigins {
		t.Error("AllowAllOrigins = false, want true for \"*\"")
	}
	if !c.OriginAllowed("https://anything.example") {
		t.Error("OriginAllowed = false, want true under wildcard")
	}
}

func TestWithAllowedOriginsExactList(t *testing.T) {
	c := Apply(WithAllowedOrigins("https://a.example", "https://b.example"))
	if !c.OriginAllowed("https://a.example") {
		t.Error("expected https://a.example to be allowed")
	}
	if c.OriginAllowed("https://evil.example") {
		t.Error("expected https://evil.example to be rejected")
	}
	if c.OriginAllowed("") {
		t.Error("empty origin must never be \"allowed\" (nothing to echo back)")
	}
}

func TestSafeMapErrorRecoversPanic(t *testing.T) {
	c := Default()
	c.MapError = func(error) string { panic("boom") }
	got := c.SafeMapError(errors.New("x"))
	if got != "internal error" {
		t.Errorf("SafeMapError after panic = %q, want the generic fallback", got)
	}
}

func TestWithErrorMapperAppliesCustomMapper(t *testing.T) {
	c := Apply(WithErrorMapper(func(error) string { return "custom" }))
	if got := c.MapError(errors.New("x")); got != "custom" {
		t.Errorf("MapError(x) = %q, want %q from the custom mapper", got, "custom")
	}
}

func TestSafeMapErrorOnBareConfig(t *testing.T) {
	// A Config's fields are all exported, so callers can build one as a
	// struct literal without Default()/Apply(), leaving Logger and
	// MapError nil. SafeMapError must not panic in either case.
	t.Run("nil MapError", func(t *testing.T) {
		c := &Config{}
		got := c.SafeMapError(errors.New("x"))
		if got != "internal error" {
			t.Errorf("SafeMapError with nil MapError = %q, want %q", got, "internal error")
		}
	})

	t.Run("panicking MapError, nil Logger", func(t *testing.T) {
		c := &Config{MapError: func(error) string { panic("boom") }}
		got := c.SafeMapError(errors.New("x"))
		if got != "internal error" {
			t.Errorf("SafeMapError with nil Logger and panicking MapError = %q, want %q", got, "internal error")
		}
	})
}
