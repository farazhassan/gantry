package taskmanager

import (
	"context"
	"sync"
)

// ReadyQueue holds session ids whose active task is ready to be driven but has
// no caller currently running it (spawned cross-session work). FIFO. The
// interface is deliberately minimal (Enqueue/Dequeue, no claim/ack): a reliable
// backend may add redelivery later, but the in-memory impl cannot meaningfully
// exercise it.
type ReadyQueue interface {
	Enqueue(ctx context.Context, sessionID string) error
	Dequeue(ctx context.Context) (sessionID string, ok bool, err error)
}

// InMemoryReadyQueue is a process-local FIFO ReadyQueue backed by a slice and
// mutex. A dequeued item is gone (no claim/ack).
type InMemoryReadyQueue struct {
	mu sync.Mutex
	q  []string
}

// NewInMemoryReadyQueue returns an empty in-memory ready queue.
func NewInMemoryReadyQueue() *InMemoryReadyQueue {
	return &InMemoryReadyQueue{}
}

// Enqueue appends a session id to the tail.
func (r *InMemoryReadyQueue) Enqueue(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.q = append(r.q, sessionID)
	return nil
}

// Dequeue pops the head, returning ok=false when empty.
func (r *InMemoryReadyQueue) Dequeue(_ context.Context) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.q) == 0 {
		return "", false, nil
	}
	sid := r.q[0]
	r.q = r.q[1:]
	return sid, true, nil
}
