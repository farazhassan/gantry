package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/farazhassan/gantry"
)

// RecordedTurn is a captured request/response pair (plus error, if any).
type RecordedTurn struct {
	Request  gantry.LLMRequest  `json:"request"`
	Response gantry.LLMResponse `json:"response"`
	Err      string             `json:"err,omitempty"`
}

// RecordingLLMClient wraps an upstream LLMClient and records every turn.
type RecordingLLMClient struct {
	upstream gantry.LLMClient
	mu       sync.Mutex
	turns    []RecordedTurn
}

// NewRecordingLLMClient wraps upstream.
func NewRecordingLLMClient(upstream gantry.LLMClient) *RecordingLLMClient {
	return &RecordingLLMClient{upstream: upstream}
}

func (r *RecordingLLMClient) Generate(ctx context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error) {
	resp, err := r.upstream.Generate(ctx, req)
	r.mu.Lock()
	t := RecordedTurn{Request: req, Response: resp}
	if err != nil {
		t.Err = err.Error()
	}
	r.turns = append(r.turns, t)
	r.mu.Unlock()
	return resp, err
}

// Recording returns a copy of the recorded turns.
func (r *RecordingLLMClient) Recording() []RecordedTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedTurn, len(r.turns))
	copy(out, r.turns)
	return out
}

// WriteJSONL writes the recording as JSONL (one turn per line).
func (r *RecordingLLMClient) WriteJSONL(w io.Writer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	enc := json.NewEncoder(w)
	for _, t := range r.turns {
		if err := enc.Encode(t); err != nil {
			return err
		}
	}
	return nil
}

// LoadRecording reads JSONL from r into a slice of RecordedTurns.
func LoadRecording(r io.Reader) ([]RecordedTurn, error) {
	var out []RecordedTurn
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var t RecordedTurn
		if err := json.Unmarshal(sc.Bytes(), &t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, sc.Err()
}

// ErrReplayExhausted is returned when a replay client runs out of turns.
var ErrReplayExhausted = errors.New("eval: replay exhausted")

// ReplayLLMClient replays a recording. Requests are NOT compared to the
// recorded request — replay is strictly positional.
type ReplayLLMClient struct {
	mu    sync.Mutex
	turns []RecordedTurn
	pos   int
}

// NewReplayLLMClient returns a client that replays the supplied turns in order.
func NewReplayLLMClient(turns []RecordedTurn) *ReplayLLMClient {
	return &ReplayLLMClient{turns: turns}
}

func (r *ReplayLLMClient) Generate(_ context.Context, _ gantry.LLMRequest) (gantry.LLMResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pos >= len(r.turns) {
		return gantry.LLMResponse{}, ErrReplayExhausted
	}
	t := r.turns[r.pos]
	r.pos++
	if t.Err != "" {
		return t.Response, errors.New(t.Err)
	}
	return t.Response, nil
}
