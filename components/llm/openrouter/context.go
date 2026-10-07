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
	"strings"
	"sync"
	"time"

	"github.com/farazhassan/gantry"
)

const modelsPath = "/v1/models"

var (
	maxContextRe = regexp.MustCompile(`(?i)(?:maximum context length is|limit of) ([\d,]+) tokens`)
	requestedRe  = regexp.MustCompile(`(?i)(?:resulted in|requested(?: about)?) ([\d,]+) tokens`)
	// anthropicRe matches Anthropic's "prompt is too long: N tokens > M maximum".
	anthropicRe = regexp.MustCompile(`([\d,]+) tokens > ([\d,]+) maximum`)
)

const (
	errTypeContextLength = "context_length_exceeded"
	promptTooLongPhrase  = "prompt is too long"
)

func atoiCommas(s string) int {
	n, _ := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	return n
}

// contextLengthError returns a *gantry.ContextLengthError when body is an
// overflow error. OpenRouter's error.code is the numeric HTTP status, so
// classification looks at error.metadata.error_type and at the message text —
// both OpenRouter's own and the upstream provider's, which it forwards as a
// string in error.metadata.raw. Limit/Requested are parsed from whichever text
// carries them. max_tokens_exceeded / token_limit_exceeded are not overflow.
func contextLengthError(body []byte, err error) error {
	var e struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				ErrorType string          `json:"error_type"`
				Raw       json.RawMessage `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return nil
	}
	texts := []string{e.Error.Message}
	var raw string
	if json.Unmarshal(e.Error.Metadata.Raw, &raw) == nil && raw != "" { // ignore non-string raw
		texts = append(texts, raw)
	}
	overflow := e.Error.Metadata.ErrorType == errTypeContextLength
	cle := &gantry.ContextLengthError{Err: err}
	for _, t := range texts {
		if strings.Contains(t, promptTooLongPhrase) {
			overflow = true
		}
		if m := maxContextRe.FindStringSubmatch(t); m != nil {
			overflow = true
			if cle.Limit == 0 {
				cle.Limit = atoiCommas(m[1])
			}
		}
		if m := anthropicRe.FindStringSubmatch(t); m != nil {
			if cle.Requested == 0 {
				cle.Requested = atoiCommas(m[1])
			}
			if cle.Limit == 0 {
				cle.Limit = atoiCommas(m[2])
			}
		}
		if m := requestedRe.FindStringSubmatch(t); m != nil && cle.Requested == 0 {
			cle.Requested = atoiCommas(m[1])
		}
	}
	if !overflow {
		return nil
	}
	return cle
}

// windowLookupTimeout bounds a single models lookup so one hung request cannot
// stall every concurrent run waiting on the same client.
const windowLookupTimeout = 10 * time.Second

// windowCache memoizes a successful context-window lookup. Failures are not
// cached so a transient error does not stick for the client's lifetime. A
// 1-buffered channel gates the lookup so waiters can abandon on their own
// context instead of blocking behind another caller's HTTP request. The zero
// value is ready to use.
type windowCache struct {
	once sync.Once
	gate chan struct{}
	mu   sync.Mutex // guards n only; never held across I/O
	n    int
}

func (w *windowCache) get() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.n
}

// ContextWindow returns the model's context_length from OpenRouter's model
// list (GET /v1/models), cached after the first success. It uses the model's
// top-level context_length, not top_provider's. A routing suffix such as
// ":nitro" is stripped if the exact id is not listed.
func (c *Client) ContextWindow(ctx context.Context) (int, error) {
	w := &c.ctxWindow
	if n := w.get(); n > 0 {
		return n, nil
	}
	w.once.Do(func() { w.gate = make(chan struct{}, 1) })
	select {
	case w.gate <- struct{}{}:
		defer func() { <-w.gate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if n := w.get(); n > 0 { // another caller finished while we waited
		return n, nil
	}
	ctx, cancel := context.WithTimeout(ctx, windowLookupTimeout)
	defer cancel()
	n, err := c.fetchContextWindow(ctx)
	if err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.n = n
	w.mu.Unlock()
	return n, nil
}

func (c *Client) fetchContextWindow(ctx context.Context) (int, error) {
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
	base, _, _ := strings.Cut(c.model, ":")
	for _, id := range []string{c.model, base} {
		for _, m := range list.Data {
			if m.ID == id && m.ContextLength > 0 {
				return m.ContextLength, nil
			}
		}
	}
	return 0, fmt.Errorf("openrouter: model %q not found in models list", c.model)
}
