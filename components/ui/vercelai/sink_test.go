package vercelai

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/farazhassan/gantry"
)

func TestSinkWritesSSEFrames(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	sink := s.Sink()

	if err := sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data: {"type":"start","messageId":"m1"}` + "\n\n",
		`data: {"type":"text-start","id":"m1:text:1"}` + "\n\n",
		`data: {"type":"text-delta","id":"m1:text:1","delta":"hi"}` + "\n\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing frame:\n%s\nfull output:\n%s", want, out)
		}
	}
}

func TestSinkFlushesAfterEachEvent(t *testing.T) {
	var buf bytes.Buffer
	flushed := 0
	s := NewSink(&buf, "m1")
	s.SetFlusher(func() { flushed++ })
	sink := s.Sink()
	if err := sink(gantry.Event{Type: gantry.EventDone}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if flushed == 0 {
		t.Fatal("expected flusher to be called after the event")
	}
}

func TestSinkEmitError(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	sink := s.Sink()
	if err := sink(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.PhaseStart}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	buf.Reset() // discard the "start" frame from the priming event above
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	want := `data: {"type":"error","errorText":"boom"}` + "\n\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}

func TestSinkEmitErrorEmitsStartFirst(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	// No Gantry event has been mapped yet, so "start" hasn't been emitted.
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	started := `data: {"type":"start","messageId":"m1"}` + "\n\n"
	errFrame := `data: {"type":"error","errorText":"boom"}` + "\n\n"
	if !strings.Contains(out, started) {
		t.Fatalf("expected \"start\" before \"error\"\nfull output:\n%s", out)
	}
	if strings.Index(out, started) > strings.Index(out, errFrame) {
		t.Fatalf("\"start\" must precede \"error\"\nfull output:\n%s", out)
	}
}

func TestSinkEmitErrorClosesOpenTextBlock(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	sink := s.Sink()
	if err := sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "partial"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	buf.Reset()
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"type":"text-end","id":"m1:text:1"`) {
		t.Fatalf("expected the open text block to be closed before \"error\":\n%s", out)
	}
	if strings.Index(out, "text-end") > strings.Index(out, `"type":"error"`) {
		t.Fatalf("text-end must precede error:\n%s", out)
	}
}

func TestSinkHeartbeatWritesCommentLine(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	if err := s.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got := buf.String(); got != ": ping\n\n" {
		t.Fatalf("got %q, want %q", got, ": ping\n\n")
	}
}

// TestSinkConcurrentEventsDoNotRace drives many goroutines calling the same
// Sink's EventSink concurrently -- the shape components/tool's parallel
// tool dispatch produces once two sub-agent runs have passthrough enabled
// and are invoked in the same PhaseToolExec pass. Run with -race; this test
// existing at all is the point (a data race here means Sink is unsafe for
// the exact scenario this feature introduces).
func TestSinkConcurrentEventsDoNotRace(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	sink := s.Sink()

	var wg sync.WaitGroup
	const goroutines = 8
	const eventsEach = 50
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < eventsEach; i++ {
				_ = sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "x"})
			}
		}()
	}
	wg.Wait()

	if buf.Len() == 0 {
		t.Fatal("expected SSE frames written, buffer is empty")
	}
}

func TestSinkCloseWritesDoneOnce(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "m1")
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	want := "data: [DONE]\n\n"
	if got := buf.String(); got != want {
		t.Fatalf("got %q, want %q (Close must be idempotent)", got, want)
	}
}
