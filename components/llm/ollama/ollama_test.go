package ollama_test

import (
	"testing"

	"github.com/farazhassan/gantry"
	"github.com/farazhassan/gantry/components/llm/ollama"
)

// Compile-time guarantee the client satisfies the streaming interface.
var _ gantry.StreamingLLMClient = (*ollama.Client)(nil)

func TestNewPanicsOnEmptyModel(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New(\"\"): want panic on empty model, got none")
		}
	}()
	ollama.New("")
}

func TestNewAppliesOptions(t *testing.T) {
	c := ollama.New("llama3.1", ollama.WithBaseURL("http://example:1234/"))
	// WithBaseURL must trim the trailing slash so path joins are clean.
	if got := c.BaseURL(); got != "http://example:1234" {
		t.Errorf("BaseURL = %q, want %q", got, "http://example:1234")
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	c := ollama.New("llama3.1")
	if got := c.BaseURL(); got != "http://localhost:11434" {
		t.Errorf("default BaseURL = %q, want %q", got, "http://localhost:11434")
	}
}
