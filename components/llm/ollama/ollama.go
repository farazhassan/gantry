package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/farazhassan/gantry/harness"
)

const (
	defaultBaseURL = "http://localhost:11434"
	chatPath       = "/api/chat"
	maxLineBytes   = 1 << 20 // 1 MiB; one NDJSON line can hold a whole tool-call payload
)

// Client is a harness.StreamingLLMClient backed by a local (or remote) Ollama
// server's /api/chat endpoint. It is safe for concurrent use: it holds no
// per-call state and the underlying *http.Client is concurrency-safe.
type Client struct {
	model   string
	baseURL string
	httpc   *http.Client
}

var _ harness.StreamingLLMClient = (*Client)(nil)

// Option configures a Client at construction.
type Option func(*Client)

// New returns a Client for the given Ollama model (e.g. "llama3.1"). It panics
// on an empty model, matching the lightweight constructors elsewhere in the
// repo (a missing model is a programmer error, not a runtime condition).
func New(model string, opts ...Option) *Client {
	if model == "" {
		panic("ollama: New requires a non-empty model")
	}
	c := &Client{
		model:   model,
		baseURL: defaultBaseURL,
		httpc:   &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithBaseURL points the client at a non-default Ollama endpoint. A trailing
// slash is trimmed so path joins stay clean.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithHTTPClient supplies the *http.Client used for requests — set this to
// configure timeouts/transport, or to point tests at an httptest server. A nil
// client is ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpc = h
		}
	}
}

// BaseURL returns the endpoint the client posts to (trailing slash trimmed).
func (c *Client) BaseURL() string { return c.baseURL }

// Generate sends a non-streaming /api/chat request and returns the assembled
// reply.
func (c *Client) Generate(ctx context.Context, req harness.LLMRequest) (harness.LLMResponse, error) {
	resp, err := c.post(ctx, req, false)
	if err != nil {
		return harness.LLMResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return harness.LLMResponse{}, err
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return harness.LLMResponse{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return assembleResponse(cr.Message.Content, cr.Message.ToolCalls, cr.DoneReason, cr.PromptEvalCount, cr.EvalCount), nil
}

// GenerateStream sends a streaming /api/chat request, invoking yield once per
// non-empty text delta as NDJSON lines arrive, and returns the fully
// aggregated reply. A yield error stops reading and is returned as-is so
// callers can match it with errors.Is.
func (c *Client) GenerateStream(ctx context.Context, req harness.LLMRequest, yield func(harness.StreamChunk) error) (harness.LLMResponse, error) {
	resp, err := c.post(ctx, req, true)
	if err != nil {
		return harness.LLMResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return harness.LLMResponse{}, err
	}

	var (
		content    strings.Builder
		calls      []wireToolCall
		doneReason string
		promptEval int
		evalCount  int
	)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return harness.LLMResponse{}, err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk chatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			return harness.LLMResponse{}, fmt.Errorf("ollama: decode stream chunk: %w", err)
		}
		if delta := chunk.Message.Content; delta != "" {
			content.WriteString(delta)
			if err := yield(harness.StreamChunk{TextDelta: delta}); err != nil {
				return harness.LLMResponse{}, err
			}
		}
		if len(chunk.Message.ToolCalls) > 0 {
			calls = append(calls, chunk.Message.ToolCalls...)
		}
		if chunk.Done {
			doneReason = chunk.DoneReason
			promptEval = chunk.PromptEvalCount
			evalCount = chunk.EvalCount
		}
	}
	if err := sc.Err(); err != nil {
		return harness.LLMResponse{}, fmt.Errorf("ollama: read stream: %w", err)
	}

	out := assembleResponse(content.String(), calls, doneReason, promptEval, evalCount)
	// Terminal metadata chunk (empty delta) for parity with the in-repo mock;
	// the default LLM handler ignores empty-delta chunks, so this is harmless.
	usage := out.Usage
	if err := yield(harness.StreamChunk{StopReason: out.StopReason, Usage: &usage}); err != nil {
		return harness.LLMResponse{}, err
	}
	return out, nil
}

func (c *Client) post(ctx context.Context, req harness.LLMRequest, stream bool) (*http.Response, error) {
	body, err := json.Marshal(toChatRequest(c.model, req, stream))
	if err != nil {
		return nil, fmt.Errorf("ollama: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: chat request: %w", err)
	}
	return resp, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode/100 == 2 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("ollama: chat: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
}
