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
	"time"

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
	if json.Unmarshal(body, &e) != nil || e.Error.Type != "invalid_request_error" || !strings.Contains(e.Error.Message, "prompt is too long") {
		return nil
	}
	cle := &gantry.ContextLengthError{Err: err}
	if m := promptTooLongRe.FindStringSubmatch(e.Error.Message); m != nil {
		cle.Requested, _ = strconv.Atoi(m[1])
		cle.Limit, _ = strconv.Atoi(m[2])
	}
	return cle
}

// windowLookupTimeout bounds a single models lookup so one hung request cannot
// stall every concurrent run waiting on the same client.
const windowLookupTimeout = 10 * time.Second

// windowFailureTTL is how long a failed lookup is remembered, so a persistently
// failing models endpoint is not hit on every run.
const windowFailureTTL = 5 * time.Minute

// now is the clock for failure expiry; tests replace it.
var now = time.Now

// windowCache memoizes a successful context-window lookup, and a failed one
// for windowFailureTTL (errors caused by the caller's own context are never
// cached). A 1-buffered channel gates the lookup so waiters can abandon on
// their own context instead of blocking behind another caller's HTTP request.
// The zero value is ready to use.
type windowCache struct {
	once  sync.Once
	gate  chan struct{}
	mu    sync.Mutex // guards the fields below only; never held across I/O
	n     int
	err   error
	errAt time.Time
}

// get returns a cached window, or a still-fresh cached failure, or zeros.
func (w *windowCache) get() (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.n > 0 {
		return w.n, nil
	}
	if w.err != nil && now().Sub(w.errAt) < windowFailureTTL {
		return 0, w.err
	}
	return 0, nil
}

// ContextWindow returns the model's max input tokens from the Models API
// (GET /v1/models/{model} → max_input_tokens), cached after the first success
// and a failure for a few minutes.
func (c *Client) ContextWindow(ctx context.Context) (int, error) {
	w := &c.ctxWindow
	if n, err := w.get(); n > 0 || err != nil {
		return n, err
	}
	w.once.Do(func() { w.gate = make(chan struct{}, 1) })
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if n, err := w.get(); n > 0 || err != nil { // another caller finished while we waited
		return n, err
	}
	lctx, cancel := context.WithTimeout(ctx, windowLookupTimeout)
	defer cancel()
	n, err := c.fetchContextWindow(lctx)
	if err != nil {
		if ctx.Err() == nil { // the caller's own cancellation says nothing about the endpoint
			w.mu.Lock()
			w.err, w.errAt = err, now()
			w.mu.Unlock()
		}
		return 0, err
	}
	w.mu.Lock()
	w.n, w.err = n, nil
	w.mu.Unlock()
	return n, nil
}

func (c *Client) fetchContextWindow(ctx context.Context) (int, error) {
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
	return m.MaxInputTokens, nil
}
