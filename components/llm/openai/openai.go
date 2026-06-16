package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/farazhassan/gantry"
)

const (
	defaultBaseURL = "https://api.openai.com"
	chatPath       = "/v1/chat/completions"
	apiKeyEnv      = "OPENAI_API_KEY"
	dataPrefix     = "data: "
	doneSentinel   = "[DONE]"
	maxLineBytes   = 1 << 20 // 1 MiB; one SSE data line can hold a whole tool-call payload
)

// Client is a gantry.StreamingLLMClient backed by OpenAI's
// /v1/chat/completions endpoint. It is safe for concurrent use: it holds no
// per-call state and the underlying *http.Client is concurrency-safe.
type Client struct {
	model   string
	baseURL string
	apiKey  string
	httpc   *http.Client
}

var _ gantry.StreamingLLMClient = (*Client)(nil)

// Option configures a Client at construction.
type Option func(*Client)

// New returns a Client for the given OpenAI model (e.g. "gpt-4o"). The API key
// is taken from WithAPIKey, or falls back to the OPENAI_API_KEY environment
// variable. It panics on an empty model or a missing key — both are programmer
// errors, not runtime conditions.
func New(model string, opts ...Option) *Client {
	if model == "" {
		panic("openai: New requires a non-empty model")
	}
	c := &Client{
		model:   model,
		baseURL: defaultBaseURL,
		apiKey:  os.Getenv(apiKeyEnv),
		httpc:   &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.apiKey == "" {
		panic("openai: New requires an API key (WithAPIKey or " + apiKeyEnv + ")")
	}
	return c
}

// WithAPIKey sets the bearer token, overriding the OPENAI_API_KEY environment
// variable. An empty key is ignored so the env fallback still applies.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		if key != "" {
			c.apiKey = key
		}
	}
}

// WithBaseURL points the client at a non-default endpoint (e.g. a proxy or an
// Azure/OpenAI-compatible server). A trailing slash is trimmed so path joins
// stay clean.
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

// Generate sends a non-streaming chat-completions request and returns the
// assembled reply.
func (c *Client) Generate(ctx context.Context, req gantry.LLMRequest) (gantry.LLMResponse, error) {
	resp, err := c.post(ctx, req, false)
	if err != nil {
		return gantry.LLMResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return gantry.LLMResponse{}, err
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return gantry.LLMResponse{}, fmt.Errorf("openai: decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return gantry.LLMResponse{}, fmt.Errorf("openai: response had no choices")
	}
	ch := cr.Choices[0]
	return assembleResponse(ch.Message.Content, ch.Message.ToolCalls, ch.FinishReason, toUsage(cr.Usage)), nil
}

// GenerateStream sends a streaming chat-completions request, invoking yield
// once per non-empty text delta as SSE chunks arrive, and returns the fully
// aggregated reply. A yield error stops reading and is returned as-is so
// callers can match it with errors.Is.
func (c *Client) GenerateStream(ctx context.Context, req gantry.LLMRequest, yield func(gantry.StreamChunk) error) (gantry.LLMResponse, error) {
	resp, err := c.post(ctx, req, true)
	if err != nil {
		return gantry.LLMResponse{}, err
	}
	defer resp.Body.Close()
	if err := checkStatus(resp); err != nil {
		return gantry.LLMResponse{}, err
	}

	var (
		content      strings.Builder
		calls        toolAccumulator
		finishReason string
		u            gantry.Usage
	)

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return gantry.LLMResponse{}, err
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || !bytes.HasPrefix(line, []byte(dataPrefix)) {
			continue
		}
		payload := bytes.TrimSpace(line[len(dataPrefix):])
		if string(payload) == doneSentinel {
			break
		}
		var chunk chatResponse
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return gantry.LLMResponse{}, fmt.Errorf("openai: decode stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			u = toUsage(chunk.Usage)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if delta := ch.Delta.Content; delta != "" {
			content.WriteString(delta)
			if err := yield(gantry.StreamChunk{TextDelta: delta}); err != nil {
				return gantry.LLMResponse{}, err
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			calls.add(tc)
		}
		if ch.FinishReason != "" {
			finishReason = ch.FinishReason
		}
	}
	if err := sc.Err(); err != nil {
		return gantry.LLMResponse{}, fmt.Errorf("openai: read stream: %w", err)
	}

	out := assembleResponse(content.String(), calls.calls(), finishReason, u)
	// Terminal metadata chunk (empty delta) for parity with the in-repo mock;
	// the default LLM handler ignores empty-delta chunks, so this is harmless.
	usage := out.Usage
	if err := yield(gantry.StreamChunk{StopReason: out.StopReason, Usage: &usage}); err != nil {
		return gantry.LLMResponse{}, err
	}
	return out, nil
}

// toolAccumulator stitches streamed tool-call fragments back together. OpenAI
// splits one call across many deltas keyed by Index: the first carries id/name,
// later ones append argument fragments.
type toolAccumulator struct {
	order []int
	byIdx map[int]*respToolCall
}

func (a *toolAccumulator) add(frag respToolCall) {
	if a.byIdx == nil {
		a.byIdx = make(map[int]*respToolCall)
	}
	cur, ok := a.byIdx[frag.Index]
	if !ok {
		cp := frag
		a.byIdx[frag.Index] = &cp
		a.order = append(a.order, frag.Index)
		return
	}
	if frag.ID != "" {
		cur.ID = frag.ID
	}
	if frag.Function.Name != "" {
		cur.Function.Name = frag.Function.Name
	}
	cur.Function.Arguments += frag.Function.Arguments
}

func (a *toolAccumulator) calls() []respToolCall {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]respToolCall, 0, len(a.order))
	for _, idx := range a.order {
		out = append(out, *a.byIdx[idx])
	}
	return out
}

func (c *Client) post(ctx context.Context, req gantry.LLMRequest, stream bool) (*http.Response, error) {
	body, err := json.Marshal(toChatRequest(c.model, req, stream))
	if err != nil {
		return nil, fmt.Errorf("openai: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+chatPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: chat request: %w", err)
	}
	return resp, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode/100 == 2 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("openai: chat: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
}

func toUsage(u *usage) gantry.Usage {
	if u == nil {
		return gantry.Usage{}
	}
	return gantry.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens}
}
