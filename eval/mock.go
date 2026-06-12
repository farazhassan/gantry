package eval

import (
	"context"
	"errors"
	"sync"

	"github.com/farazhassan/gantry/harness"
)

// MockTurn is one scripted turn returned by MockLLMClient.
type MockTurn struct {
	Response harness.LLMResponse
	Err      error
}

// MockLLMClient returns scripted LLMResponses in order, one per Generate call.
// After the script is exhausted, Generate returns ErrMockExhausted.
type MockLLMClient struct {
	mu       sync.Mutex
	script   []MockTurn
	pos      int
	requests []harness.LLMRequest
}

// NewMockLLMClient creates a mock with successful turns from the supplied
// responses (no errors).
func NewMockLLMClient(responses ...harness.LLMResponse) *MockLLMClient {
	turns := make([]MockTurn, len(responses))
	for i, r := range responses {
		turns[i] = MockTurn{Response: r}
	}
	return &MockLLMClient{script: turns}
}

// NewMockLLMClientFromScript supports turns that may return errors.
func NewMockLLMClientFromScript(script []MockTurn) *MockLLMClient {
	return &MockLLMClient{script: script}
}

// ErrMockExhausted is returned by Generate when the script has no more turns.
var ErrMockExhausted = errors.New("eval: MockLLMClient script exhausted")

// Generate consumes one turn from the script.
func (m *MockLLMClient) Generate(ctx context.Context, req harness.LLMRequest) (harness.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.pos >= len(m.script) {
		return harness.LLMResponse{}, ErrMockExhausted
	}
	turn := m.script[m.pos]
	m.pos++
	return turn.Response, turn.Err
}

// Requests returns a copy of every LLMRequest the mock has seen.
func (m *MockLLMClient) Requests() []harness.LLMRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]harness.LLMRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// Reset clears request history and rewinds the script position to 0.
func (m *MockLLMClient) Reset() {
	m.mu.Lock()
	m.pos = 0
	m.requests = nil
	m.mu.Unlock()
}
