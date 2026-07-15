package taskmanager

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestReadyQueueFIFO(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()

	if _, ok, err := q.Dequeue(ctx); err != nil || ok {
		t.Fatalf("empty Dequeue = (_, %v, %v), want (_, false, nil)", ok, err)
	}

	for _, sid := range []string{"sess-1", "sess-2", "sess-3"} {
		if err := q.Enqueue(ctx, sid); err != nil {
			t.Fatalf("Enqueue %q: %v", sid, err)
		}
	}
	for _, want := range []string{"sess-1", "sess-2", "sess-3"} {
		got, ok, err := q.Dequeue(ctx)
		if err != nil || !ok {
			t.Fatalf("Dequeue = (%q, %v, %v), want (%q, true, nil)", got, ok, err, want)
		}
		if got != want {
			t.Errorf("Dequeue = %q, want %q (FIFO)", got, want)
		}
	}
	if _, ok, _ := q.Dequeue(ctx); ok {
		t.Errorf("Dequeue after drain ok = true, want false")
	}
}

func TestReadyQueueConcurrent(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()
	const n = 64

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := q.Enqueue(ctx, fmt.Sprintf("s%d", i)); err != nil {
				t.Errorf("Enqueue: %v", err)
			}
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	var mu sync.Mutex
	wg = sync.WaitGroup{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sid, ok, err := q.Dequeue(ctx)
			if err != nil || !ok {
				t.Errorf("Dequeue = (_, %v, %v), want (_, true, nil)", ok, err)
				return
			}
			mu.Lock()
			if seen[sid] {
				t.Errorf("duplicate dequeue of %q", sid)
			}
			seen[sid] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Errorf("dequeued %d distinct ids, want %d", len(seen), n)
	}
}

func TestReadyQueueAckDropsClaim(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()
	if err := q.Enqueue(ctx, "s1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	sid, ok, err := q.Dequeue(ctx)
	if err != nil || !ok || sid != "s1" {
		t.Fatalf("Dequeue = (%q, %v, %v), want (s1, true, nil)", sid, ok, err)
	}
	if err := q.Ack(ctx, "s1"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	// A settled claim is gone: a later Nack must NOT resurrect it.
	if err := q.Nack(ctx, "s1"); err != nil {
		t.Fatalf("Nack after Ack: %v", err)
	}
	if _, ok, _ := q.Dequeue(ctx); ok {
		t.Errorf("Dequeue after Ack+Nack ok = true, want false (settled claim resurrected)")
	}
}

func TestReadyQueueNackRedeliversAtTail(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()
	for _, sid := range []string{"s1", "s2"} {
		if err := q.Enqueue(ctx, sid); err != nil {
			t.Fatalf("Enqueue %q: %v", sid, err)
		}
	}
	sid, ok, err := q.Dequeue(ctx) // claim s1
	if err != nil || !ok || sid != "s1" {
		t.Fatalf("Dequeue = (%q, %v, %v), want (s1, true, nil)", sid, ok, err)
	}
	if err := q.Nack(ctx, "s1"); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	first, ok1, _ := q.Dequeue(ctx)
	second, ok2, _ := q.Dequeue(ctx)
	if !ok1 || !ok2 || first != "s2" || second != "s1" {
		t.Errorf("redelivery order = (%q, %q), want (s2, s1) — Nack re-enqueues at the tail", first, second)
	}
}

func TestReadyQueueSettleUnclaimedIsNoOp(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()
	if err := q.Ack(ctx, "ghost"); err != nil {
		t.Errorf("Ack unclaimed = %v, want nil no-op", err)
	}
	if err := q.Nack(ctx, "ghost"); err != nil {
		t.Errorf("Nack unclaimed = %v, want nil no-op", err)
	}
	if _, ok, _ := q.Dequeue(ctx); ok {
		t.Errorf("Nack of an unclaimed id injected an entry")
	}
}

func TestReadyQueueDuplicateClaimsSettleIndependently(t *testing.T) {
	q := NewInMemoryReadyQueue()
	ctx := context.Background()
	// The same id enqueued twice (e.g. Recover racing an existing entry).
	if err := q.Enqueue(ctx, "s1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := q.Enqueue(ctx, "s1"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, ok, _ := q.Dequeue(ctx); !ok {
		t.Fatalf("first Dequeue empty")
	}
	if _, ok, _ := q.Dequeue(ctx); !ok {
		t.Fatalf("second Dequeue empty")
	}
	// Two outstanding claims of s1: one nacked, one acked.
	if err := q.Nack(ctx, "s1"); err != nil {
		t.Fatalf("Nack: %v", err)
	}
	if err := q.Ack(ctx, "s1"); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	sid, ok, _ := q.Dequeue(ctx)
	if !ok || sid != "s1" {
		t.Fatalf("Dequeue = (%q, %v), want the one nacked redelivery", sid, ok)
	}
	if _, ok, _ := q.Dequeue(ctx); ok {
		t.Errorf("extra entry after settling both claims")
	}
}
