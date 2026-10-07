package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"

	"github.com/farazhassan/gantry"
)

const modelsPath = "/v1/models"

var (
	maxContextRe = regexp.MustCompile(`maximum context length is (\d+) tokens`)
	requestedRe  = regexp.MustCompile(`(?:resulted in|requested(?: about)?) (\d+) tokens`)
)

// contextLengthError returns a *gantry.ContextLengthError when body is an
// overflow error. OpenRouter's error.code is the numeric HTTP status, so the
// message text is the only discriminator.
func contextLengthError(body []byte, err error) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return nil
	}
	m := maxContextRe.FindStringSubmatch(e.Error.Message)
	if m == nil {
		return nil
	}
	cle := &gantry.ContextLengthError{Err: err}
	cle.Limit, _ = strconv.Atoi(m[1])
	if r := requestedRe.FindStringSubmatch(e.Error.Message); r != nil {
		cle.Requested, _ = strconv.Atoi(r[1])
	}
	return cle
}

// windowCache memoizes a successful context-window lookup. Failures are not
// cached so a transient error does not stick for the client's lifetime.
type windowCache struct {
	mu sync.Mutex
	n  int
}

// ContextWindow returns the model's context_length from OpenRouter's model
// list (GET /v1/models), cached after the first success.
func (c *Client) ContextWindow(ctx context.Context) (int, error) {
	c.ctxWindow.mu.Lock()
	defer c.ctxWindow.mu.Unlock()
	if c.ctxWindow.n > 0 {
		return c.ctxWindow.n, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+modelsPath, nil)
	if err != nil {
		return 0, fmt.Errorf("openrouter: build models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("openrouter: models request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, fmt.Errorf("openrouter: models: status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var list struct {
		Data []struct {
			ID            string `json:"id"`
			ContextLength int    `json:"context_length"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, fmt.Errorf("openrouter: decode models response: %w", err)
	}
	for _, m := range list.Data {
		if m.ID == c.model && m.ContextLength > 0 {
			c.ctxWindow.n = m.ContextLength
			return m.ContextLength, nil
		}
	}
	return 0, fmt.Errorf("openrouter: model %q not found in models list", c.model)
}
