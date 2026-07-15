package task

import (
	"context"
	"errors"
	"testing"

	"github.com/farazhassan/gantry"
)

// streamingScriptedRunner extends the scriptedRunner fake (driver_test.go) with
// a ResumeStream that emits one text-delta Event to the sink and then applies
// the next scripted step. streamCalls counts how many runs took the streaming
// path, so tests can prove which seam the Driver chose.
type streamingScriptedRunner struct {
	scriptedRunner
	streamCalls int
}

func (r *streamingScriptedRunner) ResumeStream(ctx context.Context, prior *gantry.State, sink gantry.EventSink) (*gantry.State, error) {
	r.streamCalls++
	ev := gantry.Event{Type: gantry.EventTextDelta, TextDelta: "chunk", Iteration: r.streamCalls}
	if err := sink(ev); err != nil {
		return prior, err
	}
	return r.scriptedRunner.Resume(ctx, prior)
}

func TestAdvanceStreamsWithSinkAndStreamingRunner(t *testing.T) {
	runner := &streamingScriptedRunner{scriptedRunner: scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}}
	var events []gantry.Event
	sink := func(ev gantry.Event) error { events = append(events, ev); return nil }
	d := NewDriver(runner, NewInMemory(), WithEventSink(sink))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if runner.streamCalls != 1 {
		t.Errorf("streamCalls = %d, want 1 (Advance must take the ResumeStream seam)", runner.streamCalls)
	}
	if len(events) != 1 || events[0].Type != gantry.EventTextDelta {
		t.Errorf("sink events = %+v, want exactly one text_delta", events)
	}
}

func TestAdvanceStreamsEveryRunOfAMultiRunTask(t *testing.T) {
	runner := &streamingScriptedRunner{scriptedRunner: scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneMaxIterations, twoStepPlan()), // continuation → second run
		done(gantry.DoneNoToolCalls, nil),
	}}}
	var events []gantry.Event
	sink := func(ev gantry.Event) error { events = append(events, ev); return nil }
	d := NewDriver(runner, NewInMemory(), WithEventSink(sink))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if runner.streamCalls != 2 {
		t.Errorf("streamCalls = %d, want 2 (every run in the drive loop streams)", runner.streamCalls)
	}
	if len(events) != 2 {
		t.Errorf("sink received %d events, want 2 (one per run)", len(events))
	}
}

func TestAdvanceWithSinkFallsBackToResumeForPlainRunner(t *testing.T) {
	runner := &scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}
	var events []gantry.Event
	sink := func(ev gantry.Event) error { events = append(events, ev); return nil }
	d := NewDriver(runner, NewInMemory(), WithEventSink(sink))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if got.Status != TaskDone {
		t.Errorf("status = %q, want done", got.Status)
	}
	if runner.calls != 1 {
		t.Errorf("Resume calls = %d, want 1 (plain Runner must silently fall back)", runner.calls)
	}
	if len(events) != 0 {
		t.Errorf("sink received %d events from a non-streaming Runner, want 0", len(events))
	}
}

func TestAdvanceWithoutSinkUsesResumeEvenForStreamingRunner(t *testing.T) {
	runner := &streamingScriptedRunner{scriptedRunner: scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}}
	d := NewDriver(runner, NewInMemory()) // no WithEventSink
	tk := &Task{ID: "tk-1", Status: TaskPending}

	if _, err := d.Advance(context.Background(), tk, "do it"); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if runner.streamCalls != 0 {
		t.Errorf("streamCalls = %d, want 0 (no sink ⇒ Resume path)", runner.streamCalls)
	}
	if runner.calls != 1 {
		t.Errorf("Resume calls = %d, want 1", runner.calls)
	}
}

func TestAdvanceSinkErrorFailsTask(t *testing.T) {
	// A sink error aborts the streamed run (gantry's RunStream contract); the
	// Driver treats that like any runner error: TaskFailed + wrapped error.
	runner := &streamingScriptedRunner{scriptedRunner: scriptedRunner{steps: []func(*gantry.State) *gantry.State{
		done(gantry.DoneNoToolCalls, nil),
	}}}
	sentinel := errors.New("consumer gone")
	sink := func(gantry.Event) error { return sentinel }
	d := NewDriver(runner, NewInMemory(), WithEventSink(sink))
	tk := &Task{ID: "tk-1", Status: TaskPending}

	got, err := d.Advance(context.Background(), tk, "do it")
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want wrapped sentinel", err)
	}
	if got.Status != TaskFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
}
