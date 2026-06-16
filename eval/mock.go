package eval

import (
	"context"
	"errors"
	"sync"

	"github.com/farazhassan/gantry"
)

// MockTurn is one scripted turn returned by MockLLMClient.
type MockTurn struct {
	Response gantry.LLMResponse
	Err      error
}

// MockLLMClient returns scripted LLMResponses in order, one per Generate call.
// After the script is exhausted, Generate returns ErrMockExhausted.
type MockLLMClient struct {
	mu       sync.Mutex
	script   []MockTurn
	pos      int
	requests []gantry.LLMRequest
}

// NewMockLLMClient creates a mock with successful turns from the supplied
// responses (no errors).
func NewMockLLMClient(responses ...gantry.LLMResponse) *MockLLMClient {
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
func (m *MockLLMClient) Generate(ctx context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	if m.pos >= len(m.script) {
		return gantry.LLMResponse{}, ErrMockExhausted
	}
	turn := m.script[m.pos]
	m.pos++
	return turn.Response, turn.Err
}

// chunkSize is the number of runes per streamed text delta from the mock.
const chunkSize = 6

// GenerateStream consumes one turn from the script, streaming its Content as a
// sequence of text-delta chunks followed by a final chunk carrying StopReason
// and Usage. Concatenating every TextDelta reconstructs Content exactly.
func (m *MockLLMClient) GenerateStream(ctx context.Context, req gantry.LLMRequest, yield func(gantry.StreamChunk) error) (gantry.LLMResponse, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	if m.pos >= len(m.script) {
		m.mu.Unlock()
		return gantry.LLMResponse{}, ErrMockExhausted
	}
	turn := m.script[m.pos]
	m.pos++
	m.mu.Unlock()

	if turn.Err != nil {
		return turn.Response, turn.Err
	}
	if err := ctx.Err(); err != nil {
		return gantry.LLMResponse{}, err
	}

	for _, delta := range chunkRunes(turn.Response.Content, chunkSize) {
		if err := ctx.Err(); err != nil {
			return gantry.LLMResponse{}, err
		}
		if err := yield(gantry.StreamChunk{TextDelta: delta}); err != nil {
			return gantry.LLMResponse{}, err
		}
	}

	usage := turn.Response.Usage
	if err := yield(gantry.StreamChunk{StopReason: turn.Response.StopReason, Usage: &usage}); err != nil {
		return gantry.LLMResponse{}, err
	}
	return turn.Response, nil
}

// chunkRunes splits s into fixed-size rune chunks. Concatenating the result
// reproduces s exactly (including whitespace). Returns nil for the empty string.
func chunkRunes(s string, size int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var out []string
	for i := 0; i < len(runes); i += size {
		end := i + size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

// Requests returns a copy of every LLMRequest the mock has seen.
func (m *MockLLMClient) Requests() []gantry.LLMRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]gantry.LLMRequest, len(m.requests))
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
