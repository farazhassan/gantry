package session_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/farazhassan/gantry/components/checkpointer"
	"github.com/farazhassan/gantry/harness"
	"github.com/farazhassan/gantry/session"
)

func TestConcurrentDifferentSessions(t *testing.T) {
	const n = 8
	responses := make([]harness.LLMResponse, n)
	for i := range responses {
		responses[i] = resp(fmt.Sprintf("answer-%d", i), 1, 1)
	}
	mgr := session.NewManager(newTestAgent(t, responses...), checkpointer.NewInMemory())
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = mgr.Session(fmt.Sprintf("user-%d", i)).Run(ctx, "hello")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("session %d: %v", i, err)
		}
	}
}

func TestConcurrentSameSessionSerializes(t *testing.T) {
	mgr := session.NewManager(
		newTestAgent(t, resp("a", 1, 1), resp("b", 1, 1)),
		checkpointer.NewInMemory(),
	)
	ctx := context.Background()
	s := mgr.Session("shared")

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Run(ctx, "turn"); err != nil {
				t.Errorf("Run: %v", err)
			}
		}()
	}
	wg.Wait()

	// Two serialized turns => 4 messages persisted regardless of interleaving.
	hist, err := s.History(ctx)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 4 {
		t.Errorf("History = %d messages, want 4 (two serialized turns)", len(hist))
	}
}
