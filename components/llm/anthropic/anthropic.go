package anthropic

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
	defaultBaseURL = "https://api.anthropic.com"
	messagesPath   = "/v1/messages"
	apiKeyEnv      = "ANTHROPIC_API_KEY"
	apiVersion     = "2023-06-01"
	dataPrefix     = "data: "
	maxLineBytes   = 1 << 20 // 1 MiB; one SSE data line can hold a whole tool-call payload
)

// Client is a gantry.StreamingLLMClient backed by Anthropic's /v1/messages
// endpoint. It is safe for concurrent use: it holds no per-call state and the
// underlying *http.Client is concurrency-safe.
type Client struct {
	model   string
	baseURL string
	apiKey  string
	httpc   *http.Client
}

var _ gantry.StreamingLLMClient = (*Client)(nil)

// Option configures a Client at construction.
type Option func(*Client)

// New returns a Client for the given Anthropic model (e.g.
// "claude-sonnet-4-6"). The API key is taken from WithAPIKey, or falls back to
// the ANTHROPIC_API_KEY environment variable. It panics on an empty model or a
// missing key — both are programmer errors, not runtime conditions.
func New(model string, opts ...Option) *Client {
	if model == "" {
		panic("anthropic: New requires a non-empty model")
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
		panic("anthropic: New requires an API key (WithAPIKey or " + apiKeyEnv + ")")
	}
	return c
}

// WithAPIKey sets the API key, overriding the ANTHROPIC_API_KEY environment
// variable. An empty key is ignored so the env fallback still applies.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		if key != "" {
			c.apiKey = key
		}
	}
}

// WithBaseURL points the client at a non-default endpoint (e.g. a proxy). A
// trailing slash is trimmed so path joins stay clean.
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

// Generate sends a non-streaming /v1/messages request and returns the assembled
// reply.
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
		return gantry.LLMResponse{}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	content, calls := splitBlocks(cr.Content)
	return assembleResponse(content, calls, cr.StopReason, cr.Usage), nil
}

// streamEvent is one decoded SSE data payload. Anthropic multiplexes several
// event shapes over the stream, discriminated by Type; only the fields relevant
// to a given Type are populated.
type streamEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	Message      *streamStart `json:"message"`
	ContentBlock *streamBlock `json:"content_block"`
	Delta        *streamDelta `json:"delta"`
	Usage        *usage       `json:"usage"`
}

type streamStart struct {
	Usage usage `json:"usage"`
}

type streamBlock struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type streamDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	PartialJSON string `json:"partial_json"`
	StopReason  string `json:"stop_reason"`
}

// GenerateStream sends a streaming /v1/messages request, invoking yield once
// per non-empty text delta as SSE events arrive, and returns the fully
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
		content    strings.Builder
		tools      blockAccumulator
		stopReason string
		u          usage
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
		var ev streamEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return gantry.LLMResponse{}, fmt.Errorf("anthropic: decode stream event: %w", err)
		}
		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				u.InputTokens = ev.Message.Usage.InputTokens
			}
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				tools.start(ev.Index, ev.ContentBlock.ID, ev.ContentBlock.Name)
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					content.WriteString(ev.Delta.Text)
					if err := yield(gantry.StreamChunk{TextDelta: ev.Delta.Text}); err != nil {
						return gantry.LLMResponse{}, err
					}
				}
			case "input_json_delta":
				tools.appendArgs(ev.Index, ev.Delta.PartialJSON)
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage != nil {
				u.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			// terminal; loop will end at EOF
		}
	}
	if err := sc.Err(); err != nil {
		return gantry.LLMResponse{}, fmt.Errorf("anthropic: read stream: %w", err)
	}

	out := assembleResponse(content.String(), tools.blocks(), stopReason, u)
	// Terminal metadata chunk (empty delta) for parity with the in-repo mock;
	// the default LLM handler ignores empty-delta chunks, so this is harmless.
	usageCopy := out.Usage
	if err := yield(gantry.StreamChunk{StopReason: out.StopReason, Usage: &usageCopy}); err != nil {
		return gantry.LLMResponse{}, err
	}
	return out, nil
}

// blockAccumulator stitches streamed tool_use blocks back together. Anthropic
// announces a block (id+name) at content_block_start and then streams its input
// as input_json_delta fragments, all keyed by Index.
type blockAccumulator struct {
	order []int
	byIdx map[int]*toolAccum
}

type toolAccum struct {
	id   string
	name string
	args strings.Builder
}

func (a *blockAccumulator) start(idx int, id, name string) {
	if a.byIdx == nil {
		a.byIdx = make(map[int]*toolAccum)
	}
	if _, ok := a.byIdx[idx]; !ok {
		a.order = append(a.order, idx)
	}
	a.byIdx[idx] = &toolAccum{id: id, name: name}
}

func (a *blockAccumulator) appendArgs(idx int, frag string) {
	if a.byIdx == nil {
		return
	}
	if t, ok := a.byIdx[idx]; ok {
		t.args.WriteString(frag)
	}
}

func (a *blockAccumulator) blocks() []toolBlock {
	if len(a.order) == 0 {
		return nil
	}
	out := make([]toolBlock, 0, len(a.order))
	for _, idx := range a.order {
		t := a.byIdx[idx]
		out = append(out, toolBlock{ID: t.id, Name: t.name, Input: json.RawMessage(t.args.String())})
	}
	return out
}

func (c *Client) post(ctx context.Context, req gantry.LLMRequest, stream bool) (*http.Response, error) {
	body, err := json.Marshal(toChatRequest(c.model, req, stream))
	if err != nil {
		return nil, fmt.Errorf("anthropic: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+messagesPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: messages request: %w", err)
	}
	return resp, nil
}

func checkStatus(resp *http.Response) error {
	if resp.StatusCode/100 == 2 {
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("anthropic: messages: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
}
