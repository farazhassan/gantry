package langfuse

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultHost      = "https://cloud.langfuse.com"
	ingestionPath    = "/api/public/ingestion"
	defaultBatchSize   = 50
	defaultInterval    = 5 * time.Second
	defaultHTTPTimeout = 10 * time.Second
	// bufferCapacity is the in-memory event buffer. Intentionally not an option:
	// the drop-on-full best-effort guarantee depends on a bounded buffer.
	bufferCapacity = 1024
)

// Client implements harness.Tracer by batching trace events to Langfuse's
// ingestion API from a background goroutine. Safe for concurrent use.
type Client struct {
	publicKey string
	secretKey string
	host      string
	httpc     *http.Client
	batchSize int
	interval  time.Duration

	items   chan ingestionItem
	flushCh chan chan error
	done    chan struct{}
	closed  chan struct{}

	closeOnce sync.Once

	// mu guards traceIDs, which maps a span id to its run's trace id.
	// Populated by StartSpan (added in span.go) — unused until then.
	mu       sync.Mutex
	traceIDs map[string]string

	dropped atomic.Int64
}

// NOTE: the compile-time assertion `var _ harness.Tracer = (*Client)(nil)` is
// intentionally omitted here because StartSpan is implemented in Task 3
// (span.go). Task 3 restores the assertion alongside StartSpan.

// Option configures a Client at construction.
type Option func(*Client)

// New returns a Client. Public and secret keys are resolved from options first,
// falling back to LANGFUSE_PUBLIC_KEY / LANGFUSE_SECRET_KEY. New panics if
// either key is unresolved — a missing credential is a wiring mistake, not a
// recoverable runtime condition. New starts the background worker; callers must
// call Close (or Flush) before exit to drain buffered events.
func New(opts ...Option) *Client {
	c := &Client{
		host:      defaultHost,
		httpc:     &http.Client{Timeout: defaultHTTPTimeout},
		batchSize: defaultBatchSize,
		interval:  defaultInterval,
		items:     make(chan ingestionItem, bufferCapacity),
		flushCh:   make(chan chan error),
		done:      make(chan struct{}),
		closed:    make(chan struct{}),
		traceIDs:  map[string]string{},
	}
	// Env first; non-empty options below override.
	if v := os.Getenv("LANGFUSE_PUBLIC_KEY"); v != "" {
		c.publicKey = v
	}
	if v := os.Getenv("LANGFUSE_SECRET_KEY"); v != "" {
		c.secretKey = v
	}
	if v := os.Getenv("LANGFUSE_HOST"); v != "" {
		c.host = strings.TrimRight(v, "/")
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.publicKey == "" || c.secretKey == "" {
		panic("langfuse: New requires public and secret keys (via options or LANGFUSE_PUBLIC_KEY/LANGFUSE_SECRET_KEY)")
	}
	go c.worker()
	return c
}

// WithPublicKey sets the Langfuse public key. Empty is ignored so the
// LANGFUSE_PUBLIC_KEY fallback still applies.
func WithPublicKey(k string) Option {
	return func(c *Client) {
		if k != "" {
			c.publicKey = k
		}
	}
}

// WithSecretKey sets the Langfuse secret key. Empty is ignored so the
// LANGFUSE_SECRET_KEY fallback still applies.
func WithSecretKey(k string) Option {
	return func(c *Client) {
		if k != "" {
			c.secretKey = k
		}
	}
}

// WithHost points at a non-default Langfuse host. A trailing slash is trimmed.
func WithHost(url string) Option {
	return func(c *Client) {
		if url != "" {
			c.host = strings.TrimRight(url, "/")
		}
	}
}

// WithHTTPClient supplies the *http.Client used for ingestion. A nil client is
// ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpc = h
		}
	}
}

// WithBatchSize sets how many items trigger a flush. Non-positive is ignored.
func WithBatchSize(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.batchSize = n
		}
	}
}

// WithFlushInterval sets the maximum time between flushes. Non-positive is
// ignored.
func WithFlushInterval(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.interval = d
		}
	}
}

// Host returns the resolved ingestion host (no trailing slash).
func (c *Client) Host() string { return c.host }

// Dropped returns the number of events dropped because the buffer was full.
func (c *Client) Dropped() int64 { return c.dropped.Load() }

// enqueue adds an item without blocking. If the buffer is full the item is
// dropped and counted, so tracing never stalls the agent.
func (c *Client) enqueue(it ingestionItem) {
	select {
	case c.items <- it:
	default:
		c.dropped.Add(1)
	}
}

// Flush forces an immediate drain of buffered items. Returns nil after the
// worker has flushed, or nil immediately if the client is already closed.
func (c *Client) Flush() error {
	req := make(chan error, 1)
	select {
	case c.flushCh <- req:
		return <-req
	case <-c.closed:
		return nil
	}
}

// Close stops the worker, drains remaining items with a final flush, and waits
// for the worker to exit. Idempotent.
func (c *Client) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	<-c.closed
	return nil
}

func (c *Client) worker() {
	defer close(c.closed)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	batch := make([]ingestionItem, 0, c.batchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		c.send(batch)
		batch = batch[:0]
	}

	for {
		select {
		case it := <-c.items:
			batch = append(batch, it)
			if len(batch) >= c.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case req := <-c.flushCh:
			batch = drainInto(batch, c.items)
			flush()
			req <- nil
		case <-c.done:
			batch = drainInto(batch, c.items)
			flush()
			return
		}
	}
}

// drainInto appends every item currently available on items without blocking.
func drainInto(batch []ingestionItem, items <-chan ingestionItem) []ingestionItem {
	for {
		select {
		case it := <-items:
			batch = append(batch, it)
		default:
			return batch
		}
	}
}

// send POSTs one batch to Langfuse. Best-effort: all failures are logged and
// the batch dropped, never propagated to the agent.
func (c *Client) send(items []ingestionItem) {
	payload, err := json.Marshal(ingestionBatch{Batch: items})
	if err != nil {
		log.Printf("langfuse: marshal batch: %v", err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, c.host+ingestionPath, bytes.NewReader(payload))
	if err != nil {
		log.Printf("langfuse: build request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.publicKey, c.secretKey)

	resp, err := c.httpc.Do(req)
	if err != nil {
		log.Printf("langfuse: ingestion request failed: %v", err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		log.Printf("langfuse: ingestion returned status %d", resp.StatusCode)
	}
}
