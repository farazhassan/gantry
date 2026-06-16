package openai_test

import (
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/openai"
)

// Compile-time guarantee the client satisfies the streaming interface.
var _ gantry.StreamingLLMClient = (*openai.Client)(nil)

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(\"\"): want panic on empty model, got none")
		}
	}()
	openai.New("", openai.WithAPIKey("k"))
}

func TestNewPanicsOnMissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	defer func() {
		if recover() == nil {
			t.Error("New without key: want panic, got none")
		}
	}()
	openai.New("gpt-4o")
}

func TestNewReadsAPIKeyFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "env-key")
	// Should not panic: key supplied via environment.
	openai.New("gpt-4o")
}

func TestNewAppliesOptions(t *testing.T) {
	c := openai.New("gpt-4o", openai.WithAPIKey("k"), openai.WithBaseURL("http://example:1234/"))
	// WithBaseURL must trim the trailing slash so path joins are clean.
	if got := c.BaseURL(); got != "http://example:1234" {
		t.Errorf("BaseURL = %q, want %q", got, "http://example:1234")
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	c := openai.New("gpt-4o", openai.WithAPIKey("k"))
	if got := c.BaseURL(); got != "https://api.openai.com" {
		t.Errorf("default BaseURL = %q, want %q", got, "https://api.openai.com")
	}
}
