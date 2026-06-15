package langfuse

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// capture records ingestion batches received by a fake Langfuse server.
type capture struct {
	mu      sync.Mutex
	batches [][]ingestionItem
	authOK  bool
}

func (c *capture) items() []ingestionItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ingestionItem
	for _, b := range c.batches {
		out = append(out, b...)
	}
	return out
}

// newServerClient returns a Client pointed at a fake ingestion server, plus the
// capture it writes to. The client is Closed via t.Cleanup.
func newServerClient(t *testing.T, opts ...Option) (*Client, *capture) {
	t.Helper()
	cap := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && u == "pk" && p == "sk" {
			cap.mu.Lock()
			cap.authOK = true
			cap.mu.Unlock()
		}
		var batch ingestionBatch
		_ = json.NewDecoder(r.Body).Decode(&batch)
		cap.mu.Lock()
		cap.batches = append(cap.batches, batch.Batch)
		cap.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	base := []Option{
		WithPublicKey("pk"),
		WithSecretKey("sk"),
		WithHost(srv.URL),
		WithHTTPClient(srv.Client()),
	}
	c := New(append(base, opts...)...)
	t.Cleanup(func() { _ = c.Close() })
	return c, cap
}

func TestNewPanicsWithoutKeys(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "")
	t.Setenv("LANGFUSE_SECRET_KEY", "")
	defer func() {
		if recover() == nil {
			t.Fatal("New must panic when keys are missing")
		}
	}()
	New()
}

func TestNewReadsEnvAndHostDefault(t *testing.T) {
	t.Setenv("LANGFUSE_PUBLIC_KEY", "envpk")
	t.Setenv("LANGFUSE_SECRET_KEY", "envsk")
	t.Setenv("LANGFUSE_HOST", "")
	c := New()
	t.Cleanup(func() { _ = c.Close() })
	if c.Host() != "https://cloud.langfuse.com" {
		t.Fatalf("Host() = %q, want default cloud host", c.Host())
	}
}

func TestWithHostTrimsTrailingSlash(t *testing.T) {
	c := New(WithPublicKey("pk"), WithSecretKey("sk"), WithHost("https://lf.example.com/"))
	t.Cleanup(func() { _ = c.Close() })
	if c.Host() != "https://lf.example.com" {
		t.Fatalf("Host() = %q, want trailing slash trimmed", c.Host())
	}
}

func TestFlushSendsBufferedItemsWithAuth(t *testing.T) {
	c, cap := newServerClient(t)
	c.enqueue(traceCreateItem("t1", "root", time.Now()))
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := cap.items(); len(got) != 1 || got[0].Type != "trace-create" {
		t.Fatalf("captured = %v, want one trace-create", got)
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if !cap.authOK {
		t.Fatal("Basic auth header missing or wrong")
	}
}

func TestBatchSizeTriggersFlush(t *testing.T) {
	c, cap := newServerClient(t, WithBatchSize(3), WithFlushInterval(time.Hour))
	for i := 0; i < 3; i++ {
		c.enqueue(traceCreateItem("t1", "n", time.Now()))
	}
	waitFor(t, func() bool { return len(cap.items()) == 3 })
}

func TestFlushIntervalTriggersFlush(t *testing.T) {
	c, cap := newServerClient(t, WithBatchSize(1000), WithFlushInterval(20*time.Millisecond))
	c.enqueue(traceCreateItem("t1", "n", time.Now()))
	waitFor(t, func() bool { return len(cap.items()) == 1 })
}

func TestCloseDrainsRemaining(t *testing.T) {
	c, cap := newServerClient(t, WithBatchSize(1000), WithFlushInterval(time.Hour))
	c.enqueue(traceCreateItem("t1", "n", time.Now()))
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := cap.items(); len(got) != 1 {
		t.Fatalf("after Close captured %d items, want 1", len(got))
	}
}

func TestEnqueueDropsWhenFull(t *testing.T) {
	// Stop the worker so nothing drains the buffer, then overflow it.
	c := New(WithPublicKey("pk"), WithSecretKey("sk"))
	_ = c.Close() // worker exits; buffer no longer drained
	for i := 0; i < bufferCapacity+10; i++ {
		c.enqueue(traceCreateItem("t1", "n", time.Now()))
	}
	if c.Dropped() == 0 {
		t.Fatal("expected dropped count > 0 when buffer is full")
	}
}

func TestFlushSwallowsServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	c := New(WithPublicKey("pk"), WithSecretKey("sk"), WithHost(srv.URL), WithHTTPClient(srv.Client()))
	t.Cleanup(func() { _ = c.Close() })

	c.enqueue(traceCreateItem("t1", "n", time.Now()))
	// A 500 from ingestion must be swallowed: Flush returns nil, no panic.
	if err := c.Flush(); err != nil {
		t.Fatalf("Flush returned %v, want nil (errors are best-effort/logged)", err)
	}
}

// waitFor polls cond up to ~1s.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
