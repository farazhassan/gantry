package agui

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/farazhassan/gantry"
)

// tsPattern matches a JSON `"timestamp":<digits>` field — EmitError stamps
// every frame with time.Now(), so tests that assert its output can't hard-code
// an exact timestamp value the way they can every other (deterministic) field.
const tsPattern = `"timestamp":[0-9]+`

func TestSinkWritesSSEFrames(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()

	if err := sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "hi", RunID: "r1"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	out := buf.String()
	// First frame must be RUN_STARTED (lazy), then text start + content.
	for _, want := range []string{
		`data: {"type":"RUN_STARTED","threadId":"t1","runId":"r1"}` + "\n\n",
		`data: {"type":"TEXT_MESSAGE_START","messageId":"r1:msg:1","role":"assistant","runId":"r1"}` + "\n\n",
		`data: {"type":"TEXT_MESSAGE_CONTENT","messageId":"r1:msg:1","delta":"hi","runId":"r1"}` + "\n\n",
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
	if err := sink(gantry.Event{Type: gantry.EventDone}); err != nil {
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
	if err := sink(gantry.Event{Type: gantry.EventPhaseStart, Phase: gantry.Phase("start")}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	buf.Reset() // discard the RUN_STARTED + STEP_STARTED frames
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	want := regexp.MustCompile(`^data: \{"type":"RUN_ERROR","message":"boom",` + tsPattern + `\}\n\n$`)
	if !want.MatchString(buf.String()) {
		t.Fatalf("got  %q\nwant match for %s", buf.String(), want)
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
	started := regexp.MustCompile(`\{"type":"RUN_STARTED","threadId":"t1","runId":"r1",` + tsPattern + `\}`)
	runErr := regexp.MustCompile(`\{"type":"RUN_ERROR","message":"boom",` + tsPattern + `\}`)
	startedLoc := started.FindStringIndex(out)
	runErrLoc := runErr.FindStringIndex(out)
	if startedLoc == nil {
		t.Fatalf("expected RUN_STARTED before RUN_ERROR\nfull output:\n%s", out)
	}
	if runErrLoc == nil || startedLoc[0] > runErrLoc[0] {
		t.Fatalf("RUN_STARTED must precede RUN_ERROR\nfull output:\n%s", out)
	}
}

func TestSinkEmitErrorClosesOpenTextMessage(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()
	// Open a text message but never let the run finish normally.
	if err := sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "partial", RunID: "r1"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	end := regexp.MustCompile(`\{"type":"TEXT_MESSAGE_END","messageId":"r1:msg:1",` + tsPattern + `,"runId":"r1"\}`)
	runErr := regexp.MustCompile(`\{"type":"RUN_ERROR","message":"boom",` + tsPattern + `\}`)
	endLoc := end.FindStringIndex(out)
	runErrLoc := runErr.FindStringIndex(out)
	if endLoc == nil {
		t.Fatalf("expected open text message to be closed before error\nfull output:\n%s", out)
	}
	if runErrLoc == nil || endLoc[0] > runErrLoc[0] {
		t.Fatalf("TEXT_MESSAGE_END must precede RUN_ERROR\nfull output:\n%s", out)
	}
}

func TestSinkEmitErrorClosesOpenReasoningMessage(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()
	// Open a reasoning message but never let the run finish normally.
	if err := sink(gantry.Event{Type: gantry.EventReasoningDelta, ReasoningDelta: "partial", RunID: "r1"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	msgEnd := regexp.MustCompile(`\{"type":"REASONING_MESSAGE_END","messageId":"r1:reasoning:1",` + tsPattern + `,"runId":"r1"\}`)
	end := regexp.MustCompile(`\{"type":"REASONING_END","messageId":"r1:reasoning:1",` + tsPattern + `,"runId":"r1"\}`)
	runErr := regexp.MustCompile(`\{"type":"RUN_ERROR","message":"boom",` + tsPattern + `\}`)
	msgEndLoc := msgEnd.FindStringIndex(out)
	endLoc := end.FindStringIndex(out)
	runErrLoc := runErr.FindStringIndex(out)
	if msgEndLoc == nil {
		t.Fatalf("expected open reasoning message to be closed before error\nfull output:\n%s", out)
	}
	if endLoc == nil {
		t.Fatalf("expected REASONING_END before error\nfull output:\n%s", out)
	}
	if runErrLoc == nil || msgEndLoc[0] > runErrLoc[0] || endLoc[0] > runErrLoc[0] {
		t.Fatalf("REASONING_MESSAGE_END/REASONING_END must precede RUN_ERROR\nfull output:\n%s", out)
	}
}

func TestSinkEmitErrorClosesOpenTextMessagesAcrossMultipleRuns(t *testing.T) {
	var buf bytes.Buffer
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()
	// Open a text message on the parent run...
	if err := sink(gantry.Event{Type: gantry.EventTextDelta, TextDelta: "parent", RunID: "r1", Agent: "orchestrator"}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	// ...and, before it closes, open a SECOND, independent text message on a
	// nested sub-agent run.
	if err := sink(gantry.Event{
		Type: gantry.EventTextDelta, TextDelta: "child", RunID: "r2",
		Agent: "investigation", ParentRunID: "r1", ParentToolCallID: "call-1",
	}); err != nil {
		t.Fatalf("sink: %v", err)
	}
	if err := s.EmitError(errors.New("boom")); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	out := buf.String()
	// closeAllText only retains each run's RunID (not its Agent/parent link),
	// so the resulting END frames carry just "runId" — see closeAllText's
	// doc comment in mapper.go.
	endR1 := regexp.MustCompile(`\{"type":"TEXT_MESSAGE_END","messageId":"r1:msg:1",` + tsPattern + `,"runId":"r1"\}`)
	endR2 := regexp.MustCompile(`\{"type":"TEXT_MESSAGE_END","messageId":"r2:msg:1",` + tsPattern + `,"runId":"r2"\}`)
	runErr := regexp.MustCompile(`\{"type":"RUN_ERROR","message":"boom",` + tsPattern + `\}`)
	endR1Loc := endR1.FindStringIndex(out)
	endR2Loc := endR2.FindStringIndex(out)
	runErrLoc := runErr.FindStringIndex(out)
	if endR1Loc == nil {
		t.Fatalf("expected r1's open text message to be closed\nfull output:\n%s", out)
	}
	if endR2Loc == nil {
		t.Fatalf("expected r2's open text message to be closed\nfull output:\n%s", out)
	}
	if runErrLoc == nil {
		t.Fatalf("expected RUN_ERROR frame\nfull output:\n%s", out)
	}
	// Map iteration order is unspecified, so don't assert an order between
	// endR1 and endR2 — only that both precede RUN_ERROR.
	if endR1Loc[0] > runErrLoc[0] {
		t.Fatalf("r1's TEXT_MESSAGE_END must precede RUN_ERROR\nfull output:\n%s", out)
	}
	if endR2Loc[0] > runErrLoc[0] {
		t.Fatalf("r2's TEXT_MESSAGE_END must precede RUN_ERROR\nfull output:\n%s", out)
	}
}

// TestSinkHeartbeatWritesSSEComment verifies Heartbeat writes a bare SSE
// comment line and flushes it -- the keep-alive ping the handler sends on
// idle so proxies/load balancers with a read-timeout don't kill the
// connection while the agent is silently thinking or mid-tool-call.
func TestSinkHeartbeatWritesSSEComment(t *testing.T) {
	var buf bytes.Buffer
	flushed := 0
	s := NewSink(&buf, "t1", "r1")
	s.SetFlusher(func() { flushed++ })

	if err := s.Heartbeat(); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got, want := buf.String(), ": ping\n\n"; got != want {
		t.Fatalf("Heartbeat wrote %q, want %q", got, want)
	}
	if flushed != 1 {
		t.Fatalf("flushed = %d, want 1", flushed)
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
	s := NewSink(&buf, "t1", "r1")
	sink := s.Sink()

	var wg sync.WaitGroup
	const goroutines = 8
	const eventsEach = 50
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		runID := "run-" + string(rune('a'+g))
		go func(runID string) {
			defer wg.Done()
			for i := 0; i < eventsEach; i++ {
				_ = sink(gantry.Event{
					Type: gantry.EventTextDelta, TextDelta: "x", RunID: runID, Agent: "investigation",
				})
			}
		}(runID)
	}
	wg.Wait()

	if buf.Len() == 0 {
		t.Fatal("expected SSE frames written, buffer is empty")
	}
}
