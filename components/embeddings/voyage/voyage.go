package voyage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const (
	defaultBaseURL = "https://api.voyageai.com"
	embedPath      = "/v1/embeddings"
	apiKeyEnv      = "VOYAGE_API_KEY"
)

// Client implements embeddings.Embeddings over Voyage's /v1/embeddings. Safe
// for concurrent use: it holds no per-call state.
type Client struct {
	model   string
	baseURL string
	apiKey  string
	httpc   *http.Client
}

// Option configures a Client at construction.
type Option func(*Client)

// New returns a Client for the given embedding model (e.g. "voyage-3"). The API
// key comes from WithAPIKey or the VOYAGE_API_KEY environment variable. It
// panics on an empty model or missing key — both are programmer errors.
func New(model string, opts ...Option) *Client {
	if model == "" {
		panic("embeddings/voyage: New requires a non-empty model")
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
		panic("embeddings/voyage: New requires an API key (WithAPIKey or " + apiKeyEnv + ")")
	}
	return c
}

// WithAPIKey sets the bearer token. An empty key is ignored so the env fallback
// still applies.
func WithAPIKey(key string) Option {
	return func(c *Client) {
		if key != "" {
			c.apiKey = key
		}
	}
}

// WithBaseURL points the client at a non-default endpoint (e.g. a proxy or test
// server). A trailing slash is trimmed.
func WithBaseURL(url string) Option {
	return func(c *Client) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithHTTPClient supplies the *http.Client used for requests. A nil client is
// ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpc = h
		}
	}
}

// BaseURL returns the endpoint the client posts to (trailing slash trimmed).
func (c *Client) BaseURL() string { return c.baseURL }

// Embed returns one vector per input text, in input order.
func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	body, err := json.Marshal(embedRequest{Model: c.model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embeddings/voyage: marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+embedPath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embeddings/voyage: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embeddings/voyage: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("embeddings/voyage: status %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("embeddings/voyage: decode response: %w", err)
	}
	if len(er.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings/voyage: got %d vectors for %d inputs", len(er.Data), len(texts))
	}

	out := make([][]float32, len(texts))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("embeddings/voyage: response index %d out of range", d.Index)
		}
		if out[d.Index] != nil {
			return nil, fmt.Errorf("embeddings/voyage: duplicate response index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	// A duplicate index (caught above) means another slot was never filled; the
	// matching length check can't catch that on its own.
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embeddings/voyage: missing vector for input %d", i)
		}
	}
	return out, nil
}
