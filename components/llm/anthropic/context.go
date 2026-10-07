package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/farazhassan/gantry"
)

const modelsPath = "/v1/models/"

// promptTooLongRe matches Anthropic's overflow message, e.g.
// "prompt is too long: 210000 tokens > 200000 maximum".
var promptTooLongRe = regexp.MustCompile(`(\d+) tokens > (\d+) maximum`)

// contextLengthError returns a *gantry.ContextLengthError when body is
// Anthropic's "prompt is too long" error, else nil.
func contextLengthError(body []byte, err error) error {
	var e struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil || !strings.Contains(e.Error.Message, "prompt is too long") {
		return nil
	}
	cle := &gantry.ContextLengthError{Err: err}
	if m := promptTooLongRe.FindStringSubmatch(e.Error.Message); m != nil {
		cle.Requested, _ = strconv.Atoi(m[1])
		cle.Limit, _ = strconv.Atoi(m[2])
	}
	return cle
}

// windowCache memoizes a successful context-window lookup. Failures are not
// cached so a transient error does not stick for the client's lifetime.
type windowCache struct {
	mu sync.Mutex
	n  int
}

// ContextWindow returns the model's max input tokens from the Models API
// (GET /v1/models/{model} → max_input_tokens), cached after the first success.
func (c *Client) ContextWindow(ctx context.Context) (int, error) {
	c.ctxWindow.mu.Lock()
	defer c.ctxWindow.mu.Unlock()
	if c.ctxWindow.n > 0 {
		return c.ctxWindow.n, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+modelsPath+url.PathEscape(c.model), nil)
	if err != nil {
		return 0, fmt.Errorf("anthropic: build models request: %w", err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", apiVersion)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("anthropic: models request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("anthropic: models: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var m struct {
		MaxInputTokens int `json:"max_input_tokens"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return 0, fmt.Errorf("anthropic: decode models response: %w", err)
	}
	if m.MaxInputTokens <= 0 {
		return 0, fmt.Errorf("anthropic: models response has no max_input_tokens for %q", c.model)
	}
	c.ctxWindow.n = m.MaxInputTokens
	return m.MaxInputTokens, nil
}
