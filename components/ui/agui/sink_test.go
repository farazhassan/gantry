package agui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/farazhassan/gantry/harness"
)

func TestSinkWritesSSEFrames(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()

	if err := sink(harness.Event{Type: harness.EventTextDelta, TextDelta: "hi"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	out := buf.String()
	// First frame must be RUN_STARTED (lazy), then text start + content.
	for _, want := range []string{
		`data: {"type":"RUN_STARTED","threadId":"t1","runId":"r1"}` + "\n\n",
		`data: {"type":"TEXT_MESSAGE_START","messageId":"r1:msg:1","role":"assistant"}` + "\n\n",
		`data: {"type":"TEXT_MESSAGE_CONTENT","messageId":"r1:msg:1","delta":"hi"}` + "\n\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing frame:\n%s\nfull output:\n%s", want, out)
		}
	}
}

func TestSinkFlushesAfterEachEvent(t *testing.T) {
	var buf bytes.Buffer
	flushed := 0
	s := NewSink(&buf, "t1", "r1")
	s.SetFlusher(func() { flushed++ })
	sink := s.Sink()
	if err := sink(harness.Event{Type: harness.EventDone}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if flushed == 0 {
		t.Fatal("expected flusher to be called after the event")
	}
}

func TestSinkEmitError(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	// Simulate an error after the run has already begun streaming, so EmitError
	// only needs to append the RUN_ERROR frame.
	sink := s.Sink()
	if err := sink(harness.Event{Type: harness.EventPhaseStart, Phase: harness.Phase("start")}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	buf.Reset() // discard the RUN_STARTED + STEP_STARTED frames
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	want := `data: {"type":"RUN_ERROR","message":"boom"}` + "\n\n"
	if buf.String() != want {
		t.Fatalf("got  %q\nwant %q", buf.String(), want)
	}
}

func TestSinkEmitErrorEmitsRunStartedFirst(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	// No Gantry event has been mapped yet, so RUN_STARTED hasn't been emitted.
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	started := `data: {"type":"RUN_STARTED","threadId":"t1","runId":"r1"}` + "\n\n"
	runErr := `data: {"type":"RUN_ERROR","message":"boom"}` + "\n\n"
	if !strings.Contains(out, started) {
		t.Fatalf("expected RUN_STARTED before RUN_ERROR\nfull output:\n%s", out)
	}
	if strings.Index(out, started) > strings.Index(out, runErr) {
		t.Fatalf("RUN_STARTED must precede RUN_ERROR\nfull output:\n%s", out)
	}
}

func TestSinkEmitErrorClosesOpenTextMessage(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()
	// Open a text message but never let the run finish normally.
	if err := sink(harness.Event{Type: harness.EventTextDelta, TextDelta: "partial"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	end := `data: {"type":"TEXT_MESSAGE_END","messageId":"r1:msg:1"}` + "\n\n"
	runErr := `data: {"type":"RUN_ERROR","message":"boom"}` + "\n\n"
	if !strings.Contains(out, end) {
		t.Fatalf("expected open text message to be closed before error\nfull output:\n%s", out)
	}
	if strings.Index(out, end) > strings.Index(out, runErr) {
		t.Fatalf("TEXT_MESSAGE_END must precede RUN_ERROR\nfull output:\n%s", out)
	}
}
