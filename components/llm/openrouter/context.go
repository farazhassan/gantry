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

// nonOverflowErrorTypes are OpenRouter error_type values that are never a
// context-window overflow, whatever the message says.
var nonOverflowErrorTypes = map[string]bool{"max_tokens_exceeded": true, "token_limit_exceeded": true}

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
	if nonOverflowErrorTypes[e.Error.Metadata.ErrorType] {
		// Explicit metadata wins over wording heuristics: these are output
		// limits that compaction cannot fix.
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

// ContextWindow returns the model's context_length from OpenRouter's model
// list (GET /v1/models), cached after the first success
// and a failure for a few minutes. It uses the model's
// top-level context_length, not top_provider's. A routing suffix such as
// ":nitro" is stripped if the exact id is not listed.
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
