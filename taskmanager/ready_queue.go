package taskmanager

import (
	"context"
	"sync"
)

// ReadyQueue holds session ids whose active task is ready to be driven but has
// no caller currently running it (spawned cross-session work). Delivery is
// claim-based (Decision L): Dequeue CLAIMS the head entry, and the claim must
// be settled — Ack drops it (consumed), Nack re-enqueues it at the tail for
// redelivery. FIFO among unclaimed entries. Settling an id with no outstanding
// claim is a harmless no-op (idempotent settlement; Nack cannot blindly inject
// entries). Durable backends should persist claims so a crashed consumer's
// claims are redelivered; the in-memory impl cannot survive a crash at all, so
// TaskManager.Recover (the durable-store scan) is the cross-process backstop.
type ReadyQueue interface {
	Enqueue(ctx context.Context, sessionID string) error
	Dequeue(ctx context.Context) (sessionID string, ok bool, err error)
	Ack(ctx context.Context, sessionID string) error
	Nack(ctx context.Context, sessionID string) error
}

// InMemoryReadyQueue is a process-local ReadyQueue backed by a slice, a claim
// counter, and a mutex. Claims live only in memory: a crashed process loses
// the queue AND the claimed set, so in-memory redelivery covers in-process
// failures only — after a restart, TaskManager.Recover rebuilds the queue from
// the durable MetaStore/TaskStore. The claim counter (not a set) lets the same
// session id be enqueued and claimed more than once concurrently (e.g. Recover
// racing an existing entry); each claim settles independently.
type InMemoryReadyQueue struct {
	mu      sync.Mutex
	q       []string
	claimed map[string]int // outstanding (unsettled) claims per session id
}

// NewInMemoryReadyQueue returns an empty in-memory ready queue.
func NewInMemoryReadyQueue() *InMemoryReadyQueue {
	return &InMemoryReadyQueue{claimed: make(map[string]int)}
}

// Enqueue appends a session id to the tail.
func (r *InMemoryReadyQueue) Enqueue(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.q = append(r.q, sessionID)
	return nil
}

// Dequeue claims the head, returning ok=false when nothing is unclaimed.
func (r *InMemoryReadyQueue) Dequeue(_ context.Context) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.q) == 0 {
		return "", false, nil
	}
	sid := r.q[0]
	r.q = r.q[1:]
	r.claimed[sid]++
	return sid, true, nil
}

// Ack settles one outstanding claim as consumed. No-op for unclaimed ids.
func (r *InMemoryReadyQueue) Ack(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.release(sessionID)
	return nil
}

// Nack settles one outstanding claim as failed, re-enqueueing the id at the
// tail for redelivery. No-op for unclaimed ids (cannot inject entries).
func (r *InMemoryReadyQueue) Nack(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.release(sessionID) {
		r.q = append(r.q, sessionID)
	}
	return nil
}

// release decrements the claim count for sessionID, reporting whether an
// outstanding claim existed. Callers must hold r.mu.
func (r *InMemoryReadyQueue) release(sessionID string) bool {
	n, ok := r.claimed[sessionID]
	if !ok {
		return false
	}
	if n <= 1 {
		delete(r.claimed, sessionID)
	} else {
		r.claimed[sessionID] = n - 1
	}
	return true
}

// Compile-time check.
var _ ReadyQueue = (*InMemoryReadyQueue)(nil)
