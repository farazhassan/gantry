package openrouter_test

import (
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openrouter"
)

// Compile-time guarantee the client satisfies the streaming interface.
var _ gantry.StreamingLLMClient = (*openrouter.Client)(nil)

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(\"\"): want panic on empty model, got none")
		}
	}()
	openrouter.New("", openrouter.WithAPIKey("k"))
}

func TestNewPanicsOnMissingAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	defer func() {
		if recover() == nil {
			t.Error("New without key: want panic, got none")
		}
	}()
	openrouter.New("anthropic/claude-sonnet-4-6")
}

func TestNewReadsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "env-key")
	// Should not panic: key supplied via environment.
	openrouter.New("anthropic/claude-sonnet-4-6")
}

func TestNewAppliesOptions(t *testing.T) {
	c := openrouter.New("anthropic/claude-sonnet-4-6", openrouter.WithAPIKey("k"), openrouter.WithBaseURL("http://example:1234/"))
	// WithBaseURL must trim the trailing slash so path joins are clean.
	if got := c.BaseURL(); got != "http://example:1234" {
		t.Errorf("BaseURL = %q, want %q", got, "http://example:1234")
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	c := openrouter.New("anthropic/claude-sonnet-4-6", openrouter.WithAPIKey("k"))
	if got := c.BaseURL(); got != "https://openrouter.ai/api" {
		t.Errorf("default BaseURL = %q, want %q", got, "https://openrouter.ai/api")
	}
}
