package gantry

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestEventDroppedJSONShape(t *testing.T) {
	// Zero Dropped must be omitted so existing consumers see no new key.
	b, err := json.Marshal(Event{Type: EventTextDelta, TextDelta: "x"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "dropped") {
		t.Errorf("zero Dropped must be omitted from JSON: %s", b)
	}
	// Non-zero Dropped serializes under the documented key.
	b, err = json.Marshal(Event{Type: EventTextDelta, Dropped: 3})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"dropped":3`) {
		t.Errorf("Dropped not serialized as \"dropped\": %s", b)
	}
}

func TestBufferedSinkDeliversInOrderAndStopDrains(t *testing.T) {
	var mu sync.Mutex
	var got []Event
	sink := func(ev Event) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		return nil
	}
	buffered, stop := NewBufferedSink(sink, 8)

	for i := 0; i < 5; i++ {
		if err := buffered(Event{Type: EventTextDelta, Iteration: i}); err != nil {
			t.Fatalf("buffered(%d): %v", i, err)
		}
	}
	stop() // must block until the consumer has drained everything

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 5 {
		t.Fatalf("delivered %d events, want 5: %+v", len(got), got)
	}
	for i, ev := range got {
		if ev.Iteration != i {
			t.Errorf("got[%d].Iteration = %d, want %d (FIFO order)", i, ev.Iteration, i)
		}
		if ev.Dropped != 0 {
			t.Errorf("got[%d].Dropped = %d, want 0 (nothing dropped)", i, ev.Dropped)
		}
	}
}

func TestBufferedSinkDropsOldestUnderBlockedConsumer(t *testing.T) {
	var mu sync.Mutex
	var got []Event
	firstArrived := make(chan struct{})
	gate := make(chan struct{})
	var first sync.Once
	sink := func(ev Event) error {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
		first.Do(func() {
			close(firstArrived) // signal: the consumer is inside the sink...
			<-gate              // ...and now blocked, with the channel buffer empty
		})
		return nil
	}
	buffered, stop := NewBufferedSink(sink, 2)

	_ = buffered(Event{Type: EventTextDelta, Iteration: 1})
	<-firstArrived // consumer holds event 1; buffer is empty; consumer is blocked

	// Fill the size-2 buffer, then overflow it twice: each overflow must evict
	// the OLDEST queued event.
	_ = buffered(Event{Type: EventTextDelta, Iteration: 2}) // buffer [2]
	_ = buffered(Event{Type: EventTextDelta, Iteration: 3}) // buffer [2 3] — full
	_ = buffered(Event{Type: EventTextDelta, Iteration: 4}) // drops 2 → buffer [3 4]
	_ = buffered(Event{Type: EventTextDelta, Iteration: 5}) // drops 3 → buffer [4 5]

	close(gate) // unblock the consumer
	stop()      // drain [4 5] and join

	mu.Lock()
	defer mu.Unlock()
	want := []int{1, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("delivered %d events (%+v), want iterations %v", len(got), got, want)
	}
	for i, ev := range got {
		if ev.Iteration != want[i] {
			t.Errorf("got[%d].Iteration = %d, want %d", i, ev.Iteration, want[i])
		}
	}
	if got[1].Dropped != 2 {
		t.Errorf("got[1].Dropped = %d, want 2 (events 2 and 3 were dropped before it)", got[1].Dropped)
	}
	if got[0].Dropped != 0 || got[2].Dropped != 0 {
		t.Errorf("unexpected Dropped counts on undropped deliveries: %+v", got)
	}
}

func TestBufferedSinkStopIsIdempotentAndDiscardsLateEvents(t *testing.T) {
	var mu sync.Mutex
	count := 0
	sink := func(Event) error {
		mu.Lock()
		count++
		mu.Unlock()
		return nil
	}
	buffered, stop := NewBufferedSink(sink, 4)

	_ = buffered(Event{Type: EventTextDelta})
	stop()
	stop() // second stop must not panic or block

	if err := buffered(Event{Type: EventTextDelta}); err != nil {
		t.Fatalf("send after stop returned error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if count != 1 {
		t.Errorf("delivered %d events, want 1 (event after stop is discarded)", count)
	}
}

func TestBufferedSinkNilSinkPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("NewBufferedSink(nil, 1) did not panic")
		}
	}()
	_, _ = NewBufferedSink(nil, 1)
}
