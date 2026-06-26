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
