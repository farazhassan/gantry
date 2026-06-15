package anthropic_test

import (
	"testing"

	"github.com/farazhassan/gantry/components/llm/anthropic"
	"github.com/farazhassan/gantry/harness"
)

// Compile-time guarantee the client satisfies the streaming interface.
var _ harness.StreamingLLMClient = (*anthropic.Client)(nil)

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(\"\"): want panic on empty model, got none")
		}
	}()
	anthropic.New("", anthropic.WithAPIKey("k"))
}

func TestNewPanicsOnMissingAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	defer func() {
		if recover() == nil {
			t.Error("New without key: want panic, got none")
		}
	}()
	anthropic.New("claude-sonnet-4-6")
}

func TestNewReadsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	// Should not panic: key supplied via environment.
	anthropic.New("claude-sonnet-4-6")
}

func TestNewAppliesOptions(t *testing.T) {
	c := anthropic.New("claude-sonnet-4-6", anthropic.WithAPIKey("k"), anthropic.WithBaseURL("http://example:1234/"))
	// WithBaseURL must trim the trailing slash so path joins are clean.
	if got := c.BaseURL(); got != "http://example:1234" {
		t.Errorf("BaseURL = %q, want %q", got, "http://example:1234")
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	c := anthropic.New("claude-sonnet-4-6", anthropic.WithAPIKey("k"))
	if got := c.BaseURL(); got != "https://api.anthropic.com" {
		t.Errorf("default BaseURL = %q, want %q", got, "https://api.anthropic.com")
	}
}
