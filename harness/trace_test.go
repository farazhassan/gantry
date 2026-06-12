package harness_test

import (
	"testing"
	"time"

	"github.com/farazhassan/gantry/harness"
)

func TestTraceRecordEvent(t *testing.T) {
	tr := harness.NewTrace()
	tr.Record(harness.TraceEvent{
		Name:      "test_event",
		Kind:      harness.KindEvent,
		StartTime: time.Now(),
		Attrs:     map[string]any{"k": "v"},
	})
	events := tr.Snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Name != "test_event" {
		t.Errorf("event name = %q", events[0].Name)
	}
}

func TestTraceConcurrentRecord(t *testing.T) {
	tr := harness.NewTrace()
	const N = 100
	done := make(chan struct{}, N)
	for i := 0; i < N; i++ {
		go func() {
			tr.Record(harness.TraceEvent{Name: "x", Kind: harness.KindEvent})
			done <- struct{}{}
		}()
	}
	for i := 0; i < N; i++ {
		<-done
	}
	if got := len(tr.Snapshot()); got != N {
		t.Errorf("got %d events, want %d", got, N)
	}
}
